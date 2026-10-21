// Package model 定义数据模型。
//
// 命名约定：
//   - Go 字段用大驼峰（导出）
//   - JSON tag 用小驼峰，与接口契约 docs/api.md 保持一致
//   - 数据库列用下划线，由 store 层负责映射
package model

import "time"

// 会话类型。
const (
	ConversationTypeDirect = 1 // 单聊
	ConversationTypeGroup  = 2 // 群聊
)

// 消息内容类型。
//
// 现在只实现文本（1），其余是预留的扩展位：
// 将来加图片消息时只需在 service 层加一个分支，表结构和接口契约都不用改。
const (
	ContentTypeText   = 1
	ContentTypeImage  = 2
	ContentTypeFile   = 3
	ContentTypeVoice  = 4
	ContentTypeSystem = 5
)

// User 用户。
type User struct {
	ID           uint64    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"` // 永不序列化到 JSON
	Nickname     string    `json:"nickname"`
	AvatarURL    string    `json:"avatarUrl"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Conversation 会话。
type Conversation struct {
	ID        uint64    `json:"id"`
	Type      uint8     `json:"type"`
	Title     string    `json:"title"`
	OwnerID   uint64    `json:"ownerId"`
	DMKey     *string   `json:"-"` // 单聊唯一键；群聊为 nil
	NextSeq   uint64    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ConversationListItem 会话列表项（带最后一条消息与未读数）。
type ConversationListItem struct {
	ID    uint64 `json:"id"`
	Type  uint8  `json:"type"`
	Title string `json:"title"`
	// OwnerID 群主 ID（单聊为 0）。
	// 前端需要它来区分「群主」与普通成员：群主不能退群，UI 上要显示不同的操作。
	OwnerID       uint64 `json:"ownerId"`
	LastMsg       string `json:"lastMsg"`
	LastMsgAt     int64  `json:"lastMsgAt"` // Unix 毫秒
	LatestSeq     uint64 `json:"latestSeq"`
	MyLastReadSeq uint64 `json:"myLastReadSeq"`
	UnreadCount   uint64 `json:"unreadCount"`
}

// Message 消息。
type Message struct {
	ID             uint64    `json:"id"`
	ConversationID uint64    `json:"conversationId"`
	Seq            uint64    `json:"seq"`
	SenderID       uint64    `json:"senderId"`
	SenderName     string    `json:"senderName,omitempty"`
	ClientMsgID    string    `json:"clientMsgId"`
	ContentType    uint8     `json:"contentType"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"-"`
	CreatedAtMs    int64     `json:"createdAt"` // 统一给前端的毫秒时间戳
}

// Member 会话成员。
type Member struct {
	UserID      uint64    `json:"userId"`
	Username    string    `json:"username"`
	Nickname    string    `json:"nickname"`
	AvatarURL   string    `json:"avatarUrl"`
	LastReadSeq uint64    `json:"lastReadSeq"`
	JoinedAt    time.Time `json:"-"`
}

// Device 登录设备（存放 refresh token 的哈希）。
type Device struct {
	ID               uint64
	UserID           uint64
	RefreshTokenHash string
	UserAgent        string
	ExpiresAt        time.Time
	CreatedAt        time.Time
}
