package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// ConversationHandler 会话接口。
type ConversationHandler struct {
	svc *service.ConversationService
}

// NewConversationHandler 构造 ConversationHandler。
func NewConversationHandler(svc *service.ConversationService) *ConversationHandler {
	return &ConversationHandler{svc: svc}
}

type createConversationReq struct {
	Type      uint8    `json:"type" binding:"required"` // 1=单聊 2=群聊
	MemberIDs []uint64 `json:"memberIds" binding:"required"`
	Title     string   `json:"title"`
}

// List 处理 GET /api/v1/conversations?limit=
func (h *ConversationHandler) List(c *gin.Context) {
	uid := MustUserID(c)
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	items, err := h.svc.List(c.Request.Context(), uid, limit)
	if err != nil {
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}
	OK(c, Page{Items: items, Total: len(items)})
}

// Create 处理 POST /api/v1/conversations
//
// type=1（单聊）：取 memberIds[0] 作为对方，复用已有会话或新建。
// type=2（群聊）：title 必填，memberIds 为初始成员。
func (h *ConversationHandler) Create(c *gin.Context) {
	var req createConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 type 与 memberIds")
		return
	}
	if len(req.MemberIDs) == 0 {
		FailBadParam(c, "memberIds 不能为空")
		return
	}

	uid := MustUserID(c)
	var convID uint64
	var err error

	switch req.Type {
	case 1:
		convID, err = h.svc.EnsureDM(c.Request.Context(), uid, req.MemberIDs[0])
	case 2:
		if req.Title == "" {
			FailBadParam(c, "群聊需要 title")
			return
		}
		convID, err = h.svc.CreateGroup(c.Request.Context(), uid, req.Title, req.MemberIDs)
	default:
		FailBadParam(c, "type 只能是 1（单聊）或 2（群聊）")
		return
	}

	if err != nil {
		if service.IsValidationError(err) {
			FailBadParam(c, err.Error())
			return
		}
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, gin.H{"conversationId": convID})
}

// Members 处理 GET /api/v1/conversations/:id/members
func (h *ConversationHandler) Members(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	uid := MustUserID(c)

	members, err := h.svc.Members(c.Request.Context(), convID, uid)
	if err != nil {
		if errors.Is(err, service.ErrNotMember) {
			Fail(c, http.StatusForbidden, CodeNotMember, "不是该会话成员")
			return
		}
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}
	OK(c, Page{Items: members, Total: len(members)})
}

// parseIDParam 解析路径里的数字 ID，失败时已写好响应。
func parseIDParam(c *gin.Context, name string) (uint64, bool) {
	v, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || v == 0 {
		FailBadParam(c, "路径参数 "+name+" 必须是正整数")
		return 0, false
	}
	return v, true
}

// parseUintQuery 解析 query 里的无符号整数，缺省返回 def。
func parseUintQuery(c *gin.Context, name string, def uint64) uint64 {
	v := c.Query(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// parseIntQuery 解析 query 里的有符号整数，缺省返回 def。
func parseIntQuery(c *gin.Context, name string, def int) int {
	v := c.Query(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
