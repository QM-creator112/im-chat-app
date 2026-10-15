package ws

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/QM-creator112/im-chat-app/server/internal/apicode"
	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// BusinessHandler 处理业务类上行消息。
//
// 定义成接口而不是让 Gateway 直接依赖 service，是为了让 Sprint 2
// （还没有消息逻辑）能先用 NopBusinessHandler 跑通，
// Sprint 3 再换成真正的实现，Gateway 一行都不用改。
type BusinessHandler interface {
	OnSend(c *Client, reqID string, data SendData)
	OnAck(c *Client, data AckData)
	OnRead(c *Client, data ReadData)
}

// NopBusinessHandler 什么都不做的实现。
//
// 给「只想知道连接通了没」的场景用（Sprint 2 的验收就是如此）。
// 它会明确回一个"未实现"错误，而不是静默吞掉 —— 静默吞掉会让客户端
// 一直等 ack，看起来像网络问题，排查成本很高。
type NopBusinessHandler struct{}

func (NopBusinessHandler) OnSend(c *Client, reqID string, _ SendData) {
	c.SendError(apicode.Internal, "消息功能尚未实现（Sprint 3）", reqID)
}
func (NopBusinessHandler) OnAck(*Client, AckData)   {}
func (NopBusinessHandler) OnRead(*Client, ReadData) {}

// Gateway WebSocket 网关。
type Gateway struct {
	hub           *Hub
	jwtSecret     string
	biz           BusinessHandler
	allowedOrigin []string
	upgrader      websocket.Upgrader
}

// NewGateway 构造网关。
//
// allowedOrigins 为空表示不校验 Origin（仅开发环境）。
// 生产环境必须传站点域名 —— 否则任何网站都能连你的 WebSocket 并冒充用户
// （WebSocket 不受同源策略保护，浏览器不会拦跨站握手）。
func NewGateway(hub *Hub, jwtSecret string, biz BusinessHandler, allowedOrigins []string) *Gateway {
	g := &Gateway{
		hub:           hub,
		jwtSecret:     jwtSecret,
		biz:           biz,
		allowedOrigin: allowedOrigins,
	}
	if g.biz == nil {
		g.biz = NopBusinessHandler{}
	}
	g.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     g.checkOrigin,
	}
	return g
}

// checkOrigin 校验 Origin 头。
func (g *Gateway) checkOrigin(r *http.Request) bool {
	if len(g.allowedOrigin) == 0 {
		return true // 开发环境：不校验
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// 非浏览器客户端（如压测脚本、curl）没有 Origin，放行
		return true
	}
	for _, allowed := range g.allowedOrigin {
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

// Handle 处理 GET /ws?token=xxx
//
// ⚠️ token 为什么走 query string 而不是 Header：
// WebSocket 握手是 HTTP 请求，但浏览器提供的 WebSocket API **不允许自定义请求头**，
// 所以无法用 `Authorization: Bearer`。可选方案只有 query 或子协议，
// query 更简单通用。
//
// 代价：token 会出现在服务器访问日志里。
// 缓解：access token 只有 30 分钟有效期，且生产环境走 WSS 加密。
func (g *Gateway) Handle(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		// 注意：这里还没升级协议，可以用普通 HTTP 响应
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    apicode.Unauthorized,
			"message": "缺少 token（/ws?token=<accessToken>）",
		})
		return
	}

	claims, err := service.ParseAccessToken(g.jwtSecret, token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    apicode.Unauthorized,
			"message": "令牌无效或已过期",
		})
		return
	}

	conn, err := g.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade 失败时它已经自己写过响应了，这里**不能再写**，
		// 否则会得到 "superfluous response.WriteHeader call"
		return
	}

	client := newClient(g.hub, conn, claims.UserID)

	// 先注册再启动协程：保证连接一建立就能收到投递的消息
	g.hub.register(client)

	go client.writePump()
	// readPump 在当前协程里跑（gin 已经为每个请求分配了协程），
	// 它会一直阻塞到连接关闭 —— 这正是我们想要的：
	// 请求的生命周期 == 连接的生命周期
	client.readPump(g.dispatch)
}

// dispatch 按消息类型分发。
func (g *Gateway) dispatch(c *Client, env *Envelope) {
	switch env.Type {
	case TypePing:
		// 心跳：回 pong。
		// 注意读超时的刷新在 readPump 里已经做了（收到任何数据都刷新），
		// 这里只需要回应客户端，让它知道链路是通的。
		c.trySend(PongPayload())

	case TypeSend:
		data, err := DecodeData[SendData](env)
		if err != nil {
			c.SendError(apicode.BadParam, "send 消息格式错误", env.ReqID)
			return
		}
		if data.ClientMsgID == "" || data.ConversationID == 0 {
			c.SendError(apicode.BadParam, "send 需要 clientMsgId 与 conversationId", env.ReqID)
			return
		}
		g.biz.OnSend(c, env.ReqID, data)

	case TypeAck:
		data, err := DecodeData[AckData](env)
		if err != nil {
			c.SendError(apicode.BadParam, "ack 消息格式错误", env.ReqID)
			return
		}
		g.biz.OnAck(c, data)

	case TypeRead:
		data, err := DecodeData[ReadData](env)
		if err != nil {
			c.SendError(apicode.BadParam, "read 消息格式错误", env.ReqID)
			return
		}
		g.biz.OnRead(c, data)

	default:
		// 未知类型明确报错，不要静默忽略 ——
		// 静默忽略会让客户端以为消息发出去了，实际上服务端根本没处理
		c.SendError(apicode.BadParam, "未知的消息类型: "+env.Type, env.ReqID)
	}
}
