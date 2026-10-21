package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// AIHandler AI 侧车相关接口（Sprint 6）。
type AIHandler struct {
	svc *service.MessageService
}

// NewAIHandler 构造 AIHandler。
func NewAIHandler(svc *service.MessageService) *AIHandler {
	return &AIHandler{svc: svc}
}

type askAIReq struct {
	// Kind 只能是 summary 或 reply，缺省按 summary
	Kind string `json:"kind"`
}

// Ask 处理 POST /api/v1/conversations/:id/ai
//
// 语义：让 AI 在会话里发一条消息（总结最近对话 / 生成回复建议）。
//
// ⚠️ 关键设计：**AI 失败也返回 200**，因为 AI 失败会被降级成一条
// "AI 助手暂时不可用"的系统消息 —— 那条消息确实写进会话了，操作在业务上成功了。
// 只有"不是成员""数据库写不进去"这类真故障才返回错误码。
func (h *AIHandler) Ask(c *gin.Context) {
	convID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	var req askAIReq
	// 允许空 body（前端点按钮时可能什么都不传）
	_ = c.ShouldBindJSON(&req)

	kind := service.AIKindSummary
	switch req.Kind {
	case "", "summary":
		kind = service.AIKindSummary
	case "reply":
		kind = service.AIKindReply
	default:
		FailBadParam(c, "kind 只能是 summary 或 reply")
		return
	}

	uid := MustUserID(c)
	msg, err := h.svc.AskAI(c.Request.Context(), convID, uid, kind)
	if err != nil {
		if errors.Is(err, service.ErrNotMember) {
			Fail(c, http.StatusForbidden, CodeNotMember, "不是该会话成员")
			return
		}
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, gin.H{"message": msg})
}
