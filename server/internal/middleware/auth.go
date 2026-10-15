// Package middleware 是 Gin 中间件。
//
// 目前只有一个：JWT 鉴权。
//
// 依赖方向：middleware → handler（用它的错误码与响应 helper）。
// handler 不反向依赖 middleware，因此不成环。
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/handler"
	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// Auth 校验 Authorization: Bearer <token>。
//
// 校验通过后把 userID 放进 gin.Context，后续 handler 用 handler.MustUserID(c) 取。
func Auth(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")

		// 期望格式：Authorization: Bearer <token>
		// 用 SplitN(…, 2) 而不是 Split：token 本身不会含空格，
		// 但万一有异常输入，SplitN 能把剩余部分完整保留，便于报错定位。
		parts := strings.SplitN(raw, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			handler.Fail(c, http.StatusUnauthorized, handler.CodeUnauthorized,
				"缺少或格式错误的 Authorization 头（应为 Bearer <token>）")
			c.Abort()
			return
		}

		claims, err := service.ParseAccessToken(jwtSecret, parts[1])
		if err != nil {
			// 故意不区分"过期"和"无效"的具体原因给客户端：
			// 客户端统一走"用 refresh token 换新 token"的流程即可
			handler.Fail(c, http.StatusUnauthorized, handler.CodeUnauthorized,
				"令牌无效或已过期")
			c.Abort()
			return
		}

		c.Set(handler.CtxUserID, claims.UserID)
		c.Next()
	}
}
