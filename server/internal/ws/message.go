// Package ws 是 WebSocket 长连接网关。
//
// 在分层中的位置与 handler 平级：都只做协议转换，不碰数据库
// （需要数据时通过注入的函数或 service 获取）。
//
// 两条铁律（docs/02-设计.md §6）：
//  1. 一条连接必须有两个协程 —— 一个读、一个写。gorilla/websocket 不允许并发写。
//  2. 必须设超时与上限，否则慢连接会把服务拖垮。
package ws

import (
	"encoding/json"
	"log/slog"

	"github.com/QM-creator112/im-chat-app/server/internal/apicode"
)

// 消息类型常量。与 docs/api.md §3.3 / §3.4 严格对应。
const (
	// ---------- 客户端 → 服务端 ----------
	TypeSend = "send" // 发消息
	TypeAck  = "ack"  // 确认收到推送
	TypeRead = "read" // 上报已读
	TypePing = "ping" // 心跳

	// ---------- 服务端 → 客户端 ----------
	TypeSendAck  = "send_ack"  // 发送成功，已分配 seq
	TypePush     = "push"      // 收到新消息
	TypeReadPush = "read_push" // 对方已读
	TypePresence = "presence"  // 在线状态变化
	TypeError    = "error"     // 错误
	TypePong     = "pong"      // 心跳回应
)

// Envelope 是所有 WebSocket 消息的统一信封。
//
// 为什么所有消息都包一层 envelope 而不是裸发业务数据：
//   - 只有一层时，客户端要靠猜字段来区分消息类型
//   - reqId 让客户端能把响应和请求配对（弱网下同一条消息可能重发多次）
type Envelope struct {
	Type  string          `json:"type"`
	ReqID string          `json:"reqId,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ---------- 上行 data ----------

// SendData 发消息。
type SendData struct {
	ClientMsgID    string `json:"clientMsgId"`
	ConversationID uint64 `json:"conversationId"`
	ContentType    uint8  `json:"contentType"`
	Content        string `json:"content"`
}

// AckData 确认收到推送。
type AckData struct {
	ConversationID uint64 `json:"conversationId"`
	Seq            uint64 `json:"seq"`
}

// ReadData 上报已读。
type ReadData struct {
	ConversationID uint64 `json:"conversationId"`
	ReadSeq        uint64 `json:"readSeq"`
}

// ---------- 下行 data ----------

// SendAckData 发送成功回执。
type SendAckData struct {
	ClientMsgID string `json:"clientMsgId"`
	Seq         uint64 `json:"seq"`
	CreatedAt   int64  `json:"createdAt"`
}

// PushData 新消息推送。
type PushData struct {
	ConversationID uint64 `json:"conversationId"`
	Seq            uint64 `json:"seq"`
	SenderID       uint64 `json:"senderId"`
	SenderName     string `json:"senderName,omitempty"`
	ContentType    uint8  `json:"contentType"`
	Content        string `json:"content"`
	CreatedAt      int64  `json:"createdAt"`
}

// ReadPushData 已读回执推送。
type ReadPushData struct {
	ConversationID uint64 `json:"conversationId"`
	UserID         uint64 `json:"userId"`
	ReadSeq        uint64 `json:"readSeq"`
}

// PresenceData 在线状态变化。
type PresenceData struct {
	UserID uint64 `json:"userId"`
	Online bool   `json:"online"`
}

// ErrorData 错误。
type ErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	ReqID   string `json:"reqId,omitempty"`
}

// ---------------- 编码工具 ----------------

// Encode 把业务数据打包成信封。
func Encode(msgType, reqID string, data any) ([]byte, error) {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return json.Marshal(Envelope{Type: msgType, ReqID: reqID, Data: raw})
}

// MustEncode 是 Encode 的"不会失败"版本。
//
// 这里之所以可以用 panic-free 的写法而不是返回 error：
// 我们编码的都是自己定义的结构体（不含 chan / func / 循环引用），
// 理论上不会失败。真失败了也是代码 bug，此时给客户端回一个 error 信封
// 比让整个连接崩掉更合适。
func MustEncode(msgType, reqID string, data any) []byte {
	b, err := Encode(msgType, reqID, data)
	if err != nil {
		slog.Error("ws 消息编码失败", "type", msgType, "err", err)
		return ErrorPayload(apicode.Internal, "服务器内部错误", reqID)
	}
	return b
}

// PongPayload 心跳回应。
func PongPayload() []byte {
	return MustEncode(TypePong, "", nil)
}

// ErrorPayload 错误信封。
//
// ⚠️ message 只用于"可以给用户看"的提示。
// 内部错误（SQL、路径、连接串）绝不能放进这里 —— 那是信息泄露。
//
// 这个函数**保证返回合法的 JSON**：即使内部编码失败，
// 也会退化成一段写死的最小错误信封。
// 因为它的调用场景本身就在"处理错误"，再抛错会让上层无所适从。
func ErrorPayload(code int, message, reqID string) []byte {
	const fallback = `{"type":"error","data":{"code":5000,"message":"服务器内部错误"}}`

	data, err := json.Marshal(ErrorData{Code: code, Message: message, ReqID: reqID})
	if err != nil {
		return []byte(fallback)
	}

	b, err := json.Marshal(Envelope{Type: TypeError, ReqID: reqID, Data: data})
	if err != nil {
		return []byte(fallback)
	}
	return b
}

// Decode 解析上行消息。
func Decode(raw []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// DecodeData 把信封里的 data 解析成具体类型。
func DecodeData[T any](e *Envelope) (T, error) {
	var out T
	if len(e.Data) == 0 {
		return out, nil
	}
	err := json.Unmarshal(e.Data, &out)
	return out, err
}
