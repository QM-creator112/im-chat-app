package service

import (
	"errors"
	"fmt"
)

// ValidationError 表示「用户输入不合法」。
//
// 为什么要专门定义一个类型，而不是靠字符串前缀判断：
//
//	handler 层需要区分两类错误 ——
//	  ① 校验错误：message 是给用户看的（"用户名长度需在 4-32 之间"），可以安全返回
//	  ② 内部错误：message 可能含 SQL、路径、连接串，**绝不能返回给客户端**
//
// 用字符串前缀（strings.HasPrefix(err.Error(), "用户名")）判断是脆弱且错误的：
// 内部错误的信息也可能以"用户名"开头，那就泄露了。
//
// 用类型断言（errors.As）才是可靠的 —— 这是 Go 的错误处理惯例。
type ValidationError struct {
	Msg string
}

// Error 实现 error 接口。
func (e *ValidationError) Error() string { return e.Msg }

// NewValidationError 构造一个校验错误。
func NewValidationError(format string, args ...any) *ValidationError {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// IsValidationError 判断错误是否属于「用户输入不合法」。
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
