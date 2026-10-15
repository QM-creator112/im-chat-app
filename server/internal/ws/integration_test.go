package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// 这是一个**真实的端到端测试**：真的起 HTTP 服务器、真的建 WebSocket 连接、
// 真的走握手与帧收发。
//
// 为什么不用 httptest 的假连接：Sprint 2 的风险恰恰在真实网络行为上
// （升级握手、读写超时、并发写 panic、背压），这些假连接测不出来。
//
// 为什么不需要数据库：WS 网关只依赖 JWT 校验，不碰 MySQL。
// 所以即使数据库还没建好，这一层也能完整验证 ——
// 这正是"先把无依赖的部分测透"的价值。

const itSecret = "integration-test-secret-at-least-32!!"

// newWSServer 起一个只挂 /ws 路由的真实 HTTP 服务器。
func newWSServer(t *testing.T, hub *Hub, biz BusinessHandler) (*httptest.Server, *Gateway) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	gw := NewGateway(hub, itSecret, biz, nil)
	r := gin.New()
	r.GET("/ws", gw.Handle)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, gw
}

// dialWS 用真实 token 建立连接。
func dialWS(t *testing.T, srv *httptest.Server, userID uint64) *websocket.Conn {
	t.Helper()

	token, err := service.SignAccessToken(itSecret, userID, time.Minute)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token

	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("WebSocket 握手失败: %v (HTTP %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// waitFor 轮询等待条件成立。
//
// 握手是异步的：Dial 返回时服务端的 register 可能还没跑完，
// 所以断言在线状态前必须轮询，不能直接断言（那会是 flaky 测试）。
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时（%v）: %s", timeout, what)
}

// TestE2EHandshakeAndPresence 验证真实握手与连接计数。
func TestE2EHandshakeAndPresence(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)

	conn := dialWS(t, srv, 42)

	waitFor(t, 2*time.Second, "用户 42 应变为在线", func() bool {
		return hub.IsOnline(42)
	})
	if got := hub.Connections(); got != 1 {
		t.Errorf("连接数应为 1，实际 %d", got)
	}

	// 同一用户再连一条（多端）
	conn2 := dialWS(t, srv, 42)
	waitFor(t, 2*time.Second, "用户 42 应有 2 条连接", func() bool {
		return hub.ConnectionCount(42) == 2
	})
	if got := hub.OnlineUsers(); got != 1 {
		t.Errorf("在线用户数应为 1（多端只算一个用户），实际 %d", got)
	}

	// 关掉一条，用户仍在线
	_ = conn.Close()
	waitFor(t, 2*time.Second, "关掉一条后应剩 1 条连接", func() bool {
		return hub.ConnectionCount(42) == 1
	})
	if !hub.IsOnline(42) {
		t.Error("还有一条连接时用户应仍在线")
	}

	// 关掉最后一条，用户离线
	_ = conn2.Close()
	waitFor(t, 2*time.Second, "全部关闭后用户应离线", func() bool {
		return !hub.IsOnline(42)
	})
}

// TestE2EPingPong 验证心跳往返。
func TestE2EPingPong(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)
	conn := dialWS(t, srv, 7)

	// 发 ping（用协议的 JSON 信封，不是 WebSocket 控制帧）
	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"ping","data":{}}`)); err != nil {
		t.Fatalf("发送 ping 失败: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 pong 失败: %v", err)
	}
	if !strings.Contains(string(data), `"type":"pong"`) {
		t.Errorf("应收到 pong，实际: %s", data)
	}
}

// TestE2EServerInitiatedCloseFrame 验证服务端主动关闭时发出了 **1000 正常关闭帧**。
//
// 这条覆盖 writePump 里 <-c.done 那个分支，而且是**真实发现过 bug 的测试**：
//
//	早期实现里 close() 会立刻 conn.Close()，把 TCP 连接掐掉，
//	于是 writePump 的关闭帧分支永远执行不到，客户端只看到
//	1006 (abnormal closure) —— 它会误以为是自己的网络坏了，
//	排查方向完全跑偏。
//
// 现在的顺序是：先发关闭帧，再由 defer 里的 closeNow() 关连接。
func TestE2EServerInitiatedCloseFrame(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)
	conn := dialWS(t, srv, 8)

	waitFor(t, 2*time.Second, "连接应建立", func() bool { return hub.IsOnline(8) })

	hub.CloseAll()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue // 还有排队的数据帧，继续读
		}

		// ✅ 必须是 1000（正常关闭），由服务端主动发出
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Logf("收到 1000 正常关闭帧，符合预期")
			return
		}
		// ❌ 1006 = 连接被直接掐断，没发关闭帧 —— 这就是那个 bug
		if websocket.IsCloseError(err, websocket.CloseAbnormalClosure) {
			t.Fatalf("收到 1006 异常断开：服务端没有发出关闭帧。"+
				"说明 closeNow() 抢在 writePump 发关闭帧之前关掉了连接。实际错误: %v", err)
		}
		t.Fatalf("连接以非预期方式结束: %v", err)
	}
}

// TestE2EMissingTokenRejected 验证无 token 握手被拒绝（真实 HTTP 401）。
func TestE2EMissingTokenRejected(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("无 token 应当握手失败")
	}
	if resp == nil {
		t.Fatal("应当返回 HTTP 响应以便查看状态码")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("应返回 401，实际 %d", resp.StatusCode)
	}
}

// TestE2EInvalidTokenRejected 验证伪造 token 被拒绝。
func TestE2EInvalidTokenRejected(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=forged.token.value"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("伪造 token 应当握手失败")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("应返回 401，实际 %d", resp.StatusCode)
	}
}

// TestE2ETokenFromWrongSecretRejected 验证用别的密钥签的 token 无法通过。
//
// 这是安全项：如果校验逻辑有误（比如没校验签名），
// 任何人都能自己签一个 token 混进来。
func TestE2ETokenFromWrongSecretRejected(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)

	token, err := service.SignAccessToken("attacker-secret-at-least-32-bytes!!", 999, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token

	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("用错误密钥签发的 token 必须被拒绝")
	}
	if resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("绝不能升级协议（说明签名校验被绕过了）")
	}
}

// TestE2EExpiredTokenRejected 验证过期 token 被拒绝。
func TestE2EExpiredTokenRejected(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)

	token, err := service.SignAccessToken(itSecret, 5, -time.Minute) // 已过期
	if err != nil {
		t.Fatal(err)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token

	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("过期 token 必须被拒绝")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("应返回 401，实际 %d", resp.StatusCode)
	}
}

// TestE2EMessageTooLargeClosesConnection 验证超大消息被拒。
//
// SetReadLimit(64KB) 生效时 gorilla 会直接关连接并返回错误，
// 防止有人发一个 1GB 的帧把服务端内存打爆。
func TestE2EMessageTooLargeClosesConnection(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)
	conn := dialWS(t, srv, 9)

	waitFor(t, 2*time.Second, "连接应建立", func() bool { return hub.IsOnline(9) })

	// 发一个远超 64KB 的消息
	huge := make([]byte, maxMessageSize+1024)
	for i := range huge {
		huge[i] = 'x'
	}
	_ = conn.WriteMessage(websocket.TextMessage, huge)

	// 服务端应当断开连接
	waitFor(t, 3*time.Second, "超大消息应导致连接被断开", func() bool {
		return !hub.IsOnline(9)
	})
}

// TestE2EMalformedJSONDoesNotCloseConnection 验证单条格式错不影响连接。
//
// 这条是有意的容错设计：收到一条垃圾消息就断开，
// 会让客户端陷入"连上就被踢"的死循环。正确做法是回错误、继续服务。
func TestE2EMalformedJSONDoesNotCloseConnection(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)
	conn := dialWS(t, srv, 11)

	waitFor(t, 2*time.Second, "连接应建立", func() bool { return hub.IsOnline(11) })

	// 先发垃圾
	if err := conn.WriteMessage(websocket.TextMessage, []byte("这不是 JSON")); err != nil {
		t.Fatal(err)
	}
	// 应当收到错误信封
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("应收到错误信封而不是被断开: %v", err)
	}
	if !strings.Contains(string(data), `"type":"error"`) {
		t.Errorf("应回错误信封，实际: %s", data)
	}

	// 连接仍然可用：再发一个 ping 应当能收到 pong
	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("连接应仍然可用: %v", err)
	}
	_, data2, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("垃圾消息之后连接应仍然可用: %v", err)
	}
	if !strings.Contains(string(data2), `"type":"pong"`) {
		t.Errorf("应收到 pong，实际: %s", data2)
	}
}

// TestE2EConcurrentClients 验证多客户端并发连接与投递。
//
// 这条覆盖真实并发场景下的注册表与投递 —— 单测里用的是假连接，
// 这里走真实网络。
func TestE2EConcurrentClients(t *testing.T) {
	hub := NewHub(nil)
	srv, _ := newWSServer(t, hub, nil)

	const n = 20
	conns := make([]*websocket.Conn, 0, n)
	for i := 0; i < n; i++ {
		conns = append(conns, dialWS(t, srv, uint64(100+i)))
	}

	waitFor(t, 5*time.Second, "所有连接应注册完成", func() bool {
		return hub.Connections() == n
	})

	// 给每个用户投递一条，应当都成功
	for i := 0; i < n; i++ {
		if got := hub.SendToUser(uint64(100+i), []byte(`{"type":"push"}`)); got != 1 {
			t.Errorf("用户 %d 应收到 1 条，实际 %d", 100+i, got)
		}
	}

	// 每个客户端都应能读到
	for i, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, err := c.ReadMessage(); err != nil {
			t.Errorf("客户端 %d 读取失败: %v", i, err)
		}
	}

	// 全部断开后清空
	for _, c := range conns {
		_ = c.Close()
	}
	waitFor(t, 5*time.Second, "全部断开后连接数应为 0", func() bool {
		return hub.Connections() == 0
	})
}
