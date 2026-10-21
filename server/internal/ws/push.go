package ws

import (
	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// 本文件让 *Hub 隐式实现 service.Pusher 接口。
//
// 为什么要单独一个薄适配层，而不是让 service 直接构造 ws 消息：
//   - WebSocket 的**消息格式**是 ws 包的职责（契约 docs/api.md §3 由它守护）
//   - service 只表达"要推给谁、推什么业务数据"，不关心 JSON 长什么样
//
// 依赖方向：main → 把 *Hub 作为 service.Pusher 注入给 MessageService。
// service 只认自己定义的接口，完全不 import ws，因此没有循环依赖。

// PushMessage 把一条新消息推给指定用户，返回成功投递的连接数。
//
// 返回 0 表示对方当前不在线 —— 调用方应视为"跳过在线投递"而非错误。
func (h *Hub) PushMessage(recipientID uint64, msg *model.Message) int {
	payload := MustEncode(TypePush, "", PushData{
		ConversationID: msg.ConversationID,
		Seq:            msg.Seq,
		SenderID:       msg.SenderID,
		SenderName:     msg.SenderName,
		ContentType:    msg.ContentType,
		Content:        msg.Content,
		CreatedAt:      msg.CreatedAtMs,
	})
	return h.SendToUser(recipientID, payload)
}

// PushRead 把已读回执推给指定用户。
func (h *Hub) PushRead(recipientID, convID, readerID, readSeq uint64) int {
	payload := MustEncode(TypeReadPush, "", ReadPushData{
		ConversationID: convID,
		UserID:         readerID,
		ReadSeq:        readSeq,
	})
	return h.SendToUser(recipientID, payload)
}
