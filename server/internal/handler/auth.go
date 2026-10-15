package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/service"
)

// AuthHandler 账号与鉴权接口。
type AuthHandler struct {
	svc *service.AuthService
}

// NewAuthHandler 构造 AuthHandler。
func NewAuthHandler(svc *service.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// ---------- 请求体 ----------

type registerReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Nickname string `json:"nickname"`
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshReq struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// Register 处理 POST /api/v1/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 username 与 password")
		return
	}

	userID, err := h.svc.Register(c.Request.Context(), service.RegisterInput{
		Username: req.Username,
		Password: req.Password,
		Nickname: req.Nickname,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUsernameTaken):
			Fail(c, http.StatusConflict, CodeUserExists, "用户名已存在")

		case service.IsValidationError(err):
			// 校验错误的 message 是写给用户看的（如"用户名长度需在 4-32 之间"），
			// 可以安全地原样返回。
			FailBadParam(c, err.Error())

		default:
			// ⚠️ 内部错误：message 可能含 SQL、文件路径、连接串 ——
			// 只能记进日志，响应里给笼统提示。
			c.Error(err) //nolint:errcheck // gin 的 Error 只做收集，不返回值
			FailInternal(c)
		}
		return
	}

	OK(c, gin.H{"userId": userID})
}

// Login 处理 POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 username 与 password")
		return
	}

	res, err := h.svc.Login(c.Request.Context(), req.Username, req.Password,
		c.GetHeader("User-Agent"))
	if err != nil {
		if errors.Is(err, service.ErrBadCredential) {
			// 故意不区分"用户不存在"和"密码错误"，避免用户名枚举
			Fail(c, http.StatusUnauthorized, CodeBadCredential, "用户名或密码错误")
			return
		}
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, gin.H{
		"accessToken":  res.AccessToken,
		"refreshToken": res.RefreshToken,
		"user":         res.User,
	})
}

// Refresh 处理 POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshReq
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadParam(c, "请求体格式错误，需要 refreshToken")
		return
	}

	access, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, service.ErrBadRefreshToken) {
			Fail(c, http.StatusUnauthorized, CodeBadRefresh, "刷新令牌无效或已过期")
			return
		}
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}

	OK(c, gin.H{"accessToken": access})
}

// Logout 处理 POST /api/v1/auth/logout（需要鉴权）
func (h *AuthHandler) Logout(c *gin.Context) {
	var req refreshReq
	// 退出登录是幂等的：请求体不合法也不该报错，直接当成功
	if err := c.ShouldBindJSON(&req); err != nil {
		OK(c, gin.H{})
		return
	}

	if err := h.svc.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		c.Error(err) //nolint:errcheck
		FailInternal(c)
		return
	}
	OK(c, gin.H{})
}
