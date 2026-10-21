package ws

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/apicode"
	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// wsOpTimeout 单条上行消息的业务处理超时。
//
// 为什么要设：上行消息在 readPump 的协程里同步处理，如果数据库卡住
// 而没有超时，这条连接的读协程就被占住了（心跳也不会被处理）。
// 5 秒足够一次正常落库，超时则放弃并回错误。
const wsOpTimeout = 5 * time.Second

// MessageBusiness 实现 BusinessHandler：把上行消息落到业务层。
//
// 它是 ws 包与 service 包之间的唯一桥梁：
//   - ws 负责协议（信封、类型、错误码）
//   - service 负责业务（幂等、落库、投递）
type MessageBusiness struct {
	svc *service.MessageService
}

// NewMessageBusiness 构造 MessageBusiness。
func NewMessageBusiness(svc *service.MessageService) *MessageBusiness {
	return &MessageBusiness{svc: svc}
}

// OnSend 处理客户端的 send 消息。
//
// 流程：落库并分配 seq（service 内部完成幂等与投递）→ 回 send_ack。
//
// 注意：投递（push 给对端）发生在 service.Send 内部，不在返回值之后。
// 这样同一会话的推送顺序与 seq 顺序一致。
func (b *MessageBusiness) OnSend(c *Client, reqID string, data SendData) {
	ctx, cancel := context.WithTimeout(context.Background(), wsOpTimeout)
	defer cancel()

	contentType := data.ContentType
	if contentType == 0 {
		contentType = 1 // 缺省按文本处理，兼容未传该字段的客户端
	}

	msg, err := b.svc.Send(ctx, service.SendMessageInput{
		SenderID:       c.UserID(),
		ConversationID: data.ConversationID,
		ClientMsgID:    data.ClientMsgID,
		ContentType:    contentType,
		Content:        data.Content,
	})
	if err != nil {
		b.sendError(c, err, reqID)
		return
	}

	c.SendJSON(TypeSendAck, reqID, SendAckData{
		ClientMsgID: msg.ClientMsgID,
		Seq:         msg.Seq,
		CreatedAt:   msg.CreatedAtMs,
	})
}

// OnRead 处理客户端的 read 消息（上报已读位置）。
func (b *MessageBusiness) OnRead(c *Client, data ReadData) {
	ctx, cancel := context.WithTimeout(context.Background(), wsOpTimeout)
	defer cancel()

	if err := b.svc.MarkRead(ctx, data.ConversationID, c.UserID(), data.ReadSeq); err != nil {
		b.sendError(c, err, "")
	}
	// 成功不回复：已读是"通知型"操作，契约里没有对应的响应消息
}

// OnAck 处理客户端的 ack（确认收到推送）。
//
// Sprint 3 只做记录，不落库 —— 消息状态机里的"已送达"
// 需要一张 message_deliveries 表（消息 × 人），属于群聊已读的范畴（Sprint 5）。
// 这里明确记录日志，而不是假装处理了。
func (b *MessageBusiness) OnAck(c *Client, data AckData) {
	slog.Debug("收到客户端 ack",
		"user_id", c.UserID(), "conversation_id", data.ConversationID, "seq", data.Seq)
}

// sendError 把业务错误映射成 WebSocket 错误信封。
//
// ⚠️ 只有「用户能看懂」的错误才把 message 返回给客户端；
// 内部错误（SQL、连接串等）只进日志，对外统一"服务器内部错误"。
func (b *MessageBusiness) sendError(c *Client, err error, reqID string) {
	switch {
	case errors.Is(err, service.ErrMessageProcessing):
		c.SendError(apicode.Processing, err.Error(), reqID)

	case errors.Is(err, service.ErrNotMember):
		c.SendError(apicode.NotMember, "不是该会话成员", reqID)

	case errors.Is(err, service.ErrEmptyContent):
		c.SendError(apicode.BadParam, "消息内容不能为空", reqID)

	case service.IsValidationError(err):
		c.SendError(apicode.BadParam, err.Error(), reqID)

	default:
		slog.Error("ws 消息业务处理失败", "user_id", c.UserID(), "err", err)
		c.SendError(apicode.Internal, "服务器内部错误", reqID)
	}
}
