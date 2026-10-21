package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// 群管理接口（Sprint 5）。

type addMembersReq struct {
	UserIDs []uint64 `json:"userIds" binding:"required"`
}

// AddMembers 处理 POST /api/v1/conversations/:id/members（拉人入群）
func (h *ConversationHandler) AddMembers(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req addMembersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 userIds 数组")
		return
	}

	uid := MustUserID(c)
	added, err := h.svc.AddMembers(c.Request.Context(), convID, uid, req.UserIDs)
	if err != nil {
		h.handleGroupErr(c, err)
		return
	}

	OK(c, gin.H{"added": added})
}

// Leave 处理 DELETE /api/v1/conversations/:id/members/me（退群）
func (h *ConversationHandler) Leave(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	uid := MustUserID(c)
	if err := h.svc.Leave(c.Request.Context(), convID, uid); err != nil {
		h.handleGroupErr(c, err)
		return
	}

	OK(c, gin.H{})
}

// handleGroupErr 群管理接口的错误映射。
func (h *ConversationHandler) handleGroupErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrConversationNotFound):
		Fail(c, http.StatusNotFound, CodeConvNotFound, "会话不存在")
	case errors.Is(err, service.ErrNotMember):
		Fail(c, http.StatusForbidden, CodeNotMember, "不是该会话成员")
	case service.IsValidationError(err):
		// 群主不能退群、只有群聊支持、成员上限等都属于"用户能懂的规则"
		FailBadParam(c, err.Error())
	default:
		c.Error(err) //nolint:errcheck
		FailInternal(c)
	}
}
