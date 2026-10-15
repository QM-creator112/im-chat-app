// Package apicode 定义业务错误码。
//
// 为什么单独一个包，而不是放在 handler 里：
//
// 错误码同时被 HTTP 层（handler）和长连接层（ws）使用。
// 如果放在 handler 里，ws 就必须 import handler —— 而 handler 是 HTTP 协议层，
// ws 是另一套协议，这个依赖方向是别扭的（而且将来 handler 若需要 ws 的能力就会成环）。
//
// 抽出一个只含常量的叶子包，两边都依赖它，方向就干净了：
//
//	handler ─┐
//	         ├─→ apicode
//	ws      ─┘
//
// 与 docs/api.md §1.2 严格对应。
package apicode

// HTTP 状态码与错误码的对应关系见 docs/api.md。
const (
	OK = 0 // 成功

	// 通用
	BadParam     = 1001 // 参数错误
	Unauthorized = 1002 // 未登录 / 令牌无效或过期
	Forbidden    = 1003 // 无权限

	// 账号
	UserExists    = 2001 // 用户名已存在
	BadCredential = 2002 // 用户名或密码错误
	BadRefresh    = 2003 // 刷新令牌无效或已过期

	// 会话与消息
	ConvNotFound = 3001 // 会话不存在
	NotMember    = 3002 // 不是该会话成员
	Processing   = 4001 // 消息正在处理中，请稍后重试

	// 服务端
	Internal = 5000 // 服务器内部错误
)
