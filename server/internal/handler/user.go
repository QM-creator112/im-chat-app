package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// UserHandler 用户相关接口。
//
// 复用 AuthService 是因为 Sprint 1 阶段用户查询还很薄；
// 等 Sprint 3 有了好友关系，再拆出独立的 UserService。
type UserHandler struct {
	svc *service.AuthService
}

// NewUserHandler 构造 UserHandler。
func NewUserHandler(svc *service.AuthService) *UserHandler {
	return &UserHandler{svc: svc}
}

// Me 处理 GET /api/v1/users/me（需要鉴权）
func (h *UserHandler) Me(c *gin.Context) {
	uid := MustUserID(c)
	if uid == 0 {
		FailUnauthorized(c, "未登录")
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// token 有效但用户被删了 —— 当成未登录处理更合理
			FailUnauthorized(c, "账号不存在")
			return
		}
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, u)
}

// Search 处理 GET /api/v1/users?keyword=xxx（需要鉴权）
func (h *UserHandler) Search(c *gin.Context) {
	keyword := c.Query("keyword")
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	users, err := h.svc.SearchUsers(c.Request.Context(), keyword, limit)
	if err != nil {
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, Page{Items: users, Total: len(users)})
}
