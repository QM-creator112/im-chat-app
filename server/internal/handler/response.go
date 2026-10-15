// Package handler 是 HTTP 协议层。
//
// 职责：解析请求、调用 service、组装响应。
// 不负责：业务规则（service 的事）、SQL（store 的事）。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 业务错误码。与 docs/api.md §1.2 严格对应。
//
// 为什么不用 HTTP 状态码表达全部语义：
// HTTP 状态码只有有限的几十个，无法区分"用户名已存在"和"用户名格式错误"。
// 业务错误码给业务留了自己的空间。
const (
	CodeOK            = 0
	CodeBadParam      = 1001
	CodeUnauthorized  = 1002
	CodeForbidden     = 1003
	CodeUserExists    = 2001
	CodeBadCredential = 2002
	CodeBadRefresh    = 2003
	CodeConvNotFound  = 3001
	CodeNotMember     = 3002
	CodeProcessing    = 4001
	CodeInternal      = 5000
)

// Response 统一响应包装。
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// OK 返回成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: CodeOK, Message: "ok", Data: data})
}

// Fail 返回失败响应。
//
// ⚠️ 注意：**不要在这里把 err.Error() 直接返回给客户端**。
// 内部错误信息可能包含 SQL、文件路径、连接串等敏感信息。
// 正确做法是：日志里记录完整错误，响应里只给一个笼统的提示。
func Fail(c *gin.Context, httpStatus, code int, message string) {
	c.JSON(httpStatus, Response{Code: code, Message: message})
}

// FailBadParam 参数错误。
func FailBadParam(c *gin.Context, message string) {
	Fail(c, http.StatusBadRequest, CodeBadParam, message)
}

// FailUnauthorized 未登录 / 令牌无效。
func FailUnauthorized(c *gin.Context, message string) {
	Fail(c, http.StatusUnauthorized, CodeUnauthorized, message)
}

// FailInternal 内部错误（对外只给笼统提示）。
func FailInternal(c *gin.Context) {
	Fail(c, http.StatusInternalServerError, CodeInternal, "服务器内部错误")
}

// Page 列表类响应的统一结构。
//
// 为什么要「包一层」而不是裸数组：
// 裸数组没地方放 total 与 nextCursor，将来加分页就得改结构、所有客户端跟着改。
// 现在先把"插槽"留出来，以后想用随时用 —— 就像装修时顺手多留一根网线。
type Page struct {
	Items      any    `json:"items"`
	Total      int    `json:"total"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// ---------------- 请求上下文 ----------------

// CtxUserID 是 gin.Context 里存放当前用户 ID 的键。
//
// 放在 handler 包而不是 middleware 包，是为了避免循环依赖：
// middleware 需要 handler 的错误码与响应helper，所以依赖方向是
// middleware → handler。若把键放在 middleware，handler 又要反过来
// 依赖 middleware，就成环了。
const CtxUserID = "user_id"

// MustUserID 从上下文取出当前用户 ID。
//
// 只应在经过 middleware.Auth 的路由里调用；取不到说明路由配置错了，
// 返回 0 让上层鉴权自然失败（而不是 panic 掉整个服务）。
func MustUserID(c *gin.Context) uint64 {
	v, ok := c.Get(CtxUserID)
	if !ok {
		return 0
	}
	if id, ok := v.(uint64); ok {
		return id
	}
	return 0
}
