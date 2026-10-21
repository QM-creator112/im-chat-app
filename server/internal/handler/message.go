package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// MessageHandler 消息接口。
type MessageHandler struct {
	svc  *service.MessageService
	conv *service.ConversationService
}

// NewMessageHandler 构造 MessageHandler。
func NewMessageHandler(svc *service.MessageService, conv *service.ConversationService) *MessageHandler {
	return &MessageHandler{svc: svc, conv: conv}
}

// History 处理 GET /api/v1/conversations/:id/messages?beforeSeq=&limit=
//
// 向前翻页：beforeSeq 是「我本地最早那条的 seq」，取比它更早的消息。
// beforeSeq 缺省为 0，表示从最新开始往前取（首次进入会话时用）。
func (h *MessageHandler) History(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	uid := MustUserID(c)
	beforeSeq := parseUintQuery(c, "beforeSeq", 0)
	limit := parseIntQuery(c, "limit", 30)

	items, hasMore, err := h.svc.History(c.Request.Context(), convID, uid, beforeSeq, limit)
	if err != nil {
		h.handleErr(c, err)
		return
	}

	OK(c, gin.H{"items": items, "hasMore": hasMore})
}

// Sync 处理 GET /api/v1/conversations/:id/sync?sinceSeq=
//
// 断线重连后的增量补拉。这是「消息不丢」在客户端的落地点。
//
// 返回字段（契约 docs/api.md §2.3）：
//
//	items     : seq > sinceSeq 的消息（按 seq 升序）
//	latestSeq : 服务端当前最大 seq，客户端用它推进本地游标
//	hasGap    : true 表示增量已不可用（被截断/超出保留窗口），
//	            客户端应改为「按会话重新拉最近 N 条」
func (h *MessageHandler) Sync(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	uid := MustUserID(c)
	sinceSeq := parseUintQuery(c, "sinceSeq", 0)

	res, err := h.svc.Sync(c.Request.Context(), convID, uid, sinceSeq)
	if err != nil {
		h.handleErr(c, err)
		return
	}

	// store 层用 make(...) 构造切片，所以 Items 不会是 nil，
	// 序列化出来一定是 []，不会是 null —— 前端不用做空值判断。
	OK(c, gin.H{
		"items":     res.Items,
		"latestSeq": res.LatestSeq,
		"hasGap":    res.HasGap,
	})
}

type markReadReq struct {
	ReadSeq uint64 `json:"readSeq" binding:"required"`
}

// MarkRead 处理 POST /api/v1/conversations/:id/read
//
// 语义：把 last_read_seq 推进到 max(当前值, readSeq)，**只前进不回退**。
func (h *MessageHandler) MarkRead(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req markReadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 readSeq")
		return
	}
	uid := MustUserID(c)

	if err := h.svc.MarkRead(c.Request.Context(), convID, uid, req.ReadSeq); err != nil {
		h.handleErr(c, err)
		return
	}
	OK(c, gin.H{})
}

// handleErr 统一的错误映射。
func (h *MessageHandler) handleErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotMember):
		Fail(c, http.StatusForbidden, CodeNotMember, "不是该会话成员")
	case service.IsValidationError(err):
		FailBadParam(c, err.Error())
	default:
		c.Error(err) //nolint:errcheck
		FailInternal(c)
	}
}
