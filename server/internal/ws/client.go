package ws

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/QM-creator112/im-chat-app/server/internal/apicode"
)

// 连接参数。
//
// 这几个值不是拍脑袋定的，约束关系是 pingPeriod < pongWait（25 < 60）：
// 服务端每 25 秒主动 ping 一次；只要客户端还活着就会回 pong，
// 而 pong 会把读超时往后推 60 秒。所以**连续丢 2 个心跳包才会判死** ——
// 单次网络抖动不会误断。
const (
	writeWait      = 10 * time.Second // 写超时：不能太长，否则慢客户端会占住协程
	pongWait       = 60 * time.Second // 读超时：超过这个时间没收到任何数据就断开
	pingPeriod     = 25 * time.Second // 主动 ping 间隔，必须小于 pongWait
	maxMessageSize = 64 * 1024        // 单条消息上限 64KB：防有人发一个 1GB 的帧把内存打爆
	sendBufferSize = 64               // 发送缓冲长度：背压的关键
)

// MessageHandler 处理一条上行业务消息。
type MessageHandler func(c *Client, env *Envelope)

// Client 表示一条 WebSocket 连接。
type Client struct {
	userID     uint64
	conn       *websocket.Conn
	hub        *Hub
	send       chan []byte // 有界通道：背压的核心
	remoteAddr string

	done         chan struct{} // 关闭信号
	closeOnce    sync.Once
	unregistered atomic.Bool
}

// newClient 构造一条连接。
func newClient(hub *Hub, conn *websocket.Conn, userID uint64) *Client {
	return &Client{
		userID:     userID,
		conn:       conn,
		hub:        hub,
		send:       make(chan []byte, sendBufferSize),
		remoteAddr: conn.RemoteAddr().String(),
		done:       make(chan struct{}),
	}
}

// UserID 返回该连接所属用户。
func (c *Client) UserID() uint64 { return c.userID }

// Close 主动关闭连接（供外部调用）。
//
// 走**优雅路径**：只发关闭信号，由 writePump 发完关闭帧后再关底层连接。
// 这样客户端能收到 1000（正常关闭）而不是 1006（异常断开），
// 从而知道"是服务端主动关的"而不是"我的网络坏了"。
func (c *Client) Close() { c.signal() }

// signal 发出关闭信号（幂等）。只关 done，不动底层连接。
func (c *Client) signal() {
	c.closeOnce.Do(func() { close(c.done) })
}

// closeNow 立刻断开：发信号 + 掐掉底层 TCP 连接。
//
// 用在两处：
//  1. 慢连接（发送缓冲已满）—— 对方已经消费不过来，没必要再讲究礼节
//  2. readPump / writePump 的 defer —— 保证底层连接一定被释放，不泄漏 fd
//
// websocket.Conn.Close 可重复调用（第二次返回错误但不 panic），
// 所以这里不做额外保护。
func (c *Client) closeNow() {
	c.signal()
	// nil 保护是为了让单元测试能构造「没有真实连接」的 Client ——
	// 背压、注册表这些逻辑本来就不依赖真实网络。
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// markUnregistered 标记「已从 Hub 注销」，保证注销逻辑只执行一次。
//
// readPump 与 writePump 都会在退出时注销，必须幂等 ——
// 否则「离线广播」会推两次，连接计数也会多扣。
func (c *Client) markUnregistered() bool {
	return c.unregistered.CompareAndSwap(false, true)
}

// SendJSON 把一个信封投给该连接。
//
// 返回 false 表示连接已关闭或缓冲已满（即已断开）。
func (c *Client) SendJSON(msgType, reqID string, data any) bool {
	return c.trySend(MustEncode(msgType, reqID, data))
}

// SendError 回一个错误信封。
func (c *Client) SendError(code int, message, reqID string) {
	c.trySend(ErrorPayload(code, message, reqID))
}

// trySend 尝试把 payload 放进发送缓冲。
//
// ⚠️ 这是「背压 / 慢连接隔离」的核心，也是面试最常追问的一段：
//
//   - 缓冲没满 → 放进去，返回 true
//   - 缓冲已满 → 说明这个客户端消费不过来（弱网卡住，或客户端拿到消息却
//     不做 ack 导致窗口不回收）。策略是**直接断开它**，让客户端重连后
//     按 seq 游标补拉消息。
//
// 为什么不选另外两种做法：
//   - 「等待」：会阻塞投递方。慢连接会把消息服务、甚至其他正常用户的投递一起拖住。
//   - 「丢弃」：会造成静默丢消息 —— 而「不丢」正是本项目对外的核心承诺。
//
// 为什么不用无界缓冲：无界缓冲只是把雪崩推迟到内存耗尽那一刻，
// 而且更糟 —— 崩溃前你连"哪里慢"都看不到。
//
// 断开是唯一"诚实"的选择：客户端知道自己断了，会重连、会补拉，消息不会丢。
func (c *Client) trySend(payload []byte) bool {
	// 已关闭就别再往里写（往已关闭 channel 写会 panic）
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.send <- payload:
		return true
	case <-c.done:
		return false
	default:
		slog.Warn("发送缓冲已满，断开慢连接",
			"user_id", c.userID, "remote", c.remoteAddr, "buf", sendBufferSize)
		// 立刻从注册表摘掉 —— 不要留"半死"状态。
		// 虽然 readPump 的 defer 也会注销（且注销是幂等的），
		// 但那是异步的：在它跑起来之前的窗口里，这个已经废掉的连接
		// 仍会被 SendToUser 选中并浪费一次投递。
		if c.hub != nil {
			c.hub.unregister(c)
		}
		// 慢连接用硬关：对方已经消费不过来，等它收关闭帧只会白等
		c.closeNow()
		return false
	}
}

// readPump 是该连接**唯一**的读协程。
//
// 退出时注销连接并关闭底层连接 —— 这两件事只在这里和 writePump 里做，
// 且都有幂等保护。
func (c *Client) readPump(onMessage MessageHandler) {
	defer func() {
		c.hub.unregister(c)
		c.closeNow()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

	// 收到 pong 就把读超时往后推 —— 这是心跳机制的"另一半"
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			// 正常关闭（客户端主动断开、页面关闭）不报警；
			// 异常关闭才值得记一笔，它是排查"为什么他收不到消息"的第一手资料
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseAbnormalClosure,
				websocket.CloseNormalClosure) {
				slog.Warn("ws 异常关闭", "user_id", c.userID, "remote", c.remoteAddr, "err", err)
			} else {
				slog.Info("ws 正常关闭", "user_id", c.userID, "remote", c.remoteAddr)
			}
			return
		}

		// 收到任何数据也刷新读超时：客户端可能不发 ping 只发业务消息
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

		env, err := Decode(data)
		if err != nil {
			c.SendError(apicode.BadParam, "消息格式错误，应为 JSON 信封", "")
			continue // 单条消息格式错不该断开连接
		}
		if onMessage != nil {
			onMessage(c, env)
		}
	}
}

// writePump 是该连接**唯一**的写协程。
//
// gorilla/websocket 明确不允许并发写，所以所有写出必须经过这里，
// 其他协程只能往 c.send 里投递。
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.hub.unregister(c)
		c.closeNow()
	}()

	for {
		select {
		case payload := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				slog.Debug("ws 写入失败，结束写协程", "user_id", c.userID, "err", err)
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Debug("ws ping 失败，结束写协程", "user_id", c.userID, "err", err)
				return
			}

		case <-c.done:
			// 这里是「优雅关闭」的关键一步：发一个正常关闭帧（1000），
			// 让对端知道是**服务端主动关的**，而不是它的网络坏了。
			//
			// ⚠️ 顺序很重要：必须先发关闭帧，再由 defer 里的 closeNow()
			// 关掉底层连接。如果先关连接（早期实现就是这样），
			// 这个分支永远写不出去，客户端只会看到 1006 异常断开 ——
			// 这个 bug 是被 integration_test.go 里的日志发现的。
			//
			// 写超时兜底：万一对方不读，10 秒后 writePump 返回，
			// defer 的 closeNow() 仍会释放连接，不会泄漏。
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
				slog.Debug("ws 发送关闭帧失败", "user_id", c.userID, "err", err)
			}
			return
		}
	}
}
