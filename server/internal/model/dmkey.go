package model

import "fmt"

// BuildDMKey 生成单聊会话的唯一键。
//
// 放在 model 包而不是 service 包的原因：
// store 层要用它来查/建单聊会话，而架构纪律是「只有 service 可以访问 store」，
// 反向依赖（store → service）是违规的。model 是无依赖的叶子包，两边都能用。
//
// 为什么要排序成「小id:大id」：
// 如果直接用 "a:b"，那么 A 发起时是 "1:2"、B 发起时是 "2:1"，
// 会被当成两个不同的键 —— 唯一索引拦不住 → 库里出现两条单聊会话。
// 排序之后，无论谁发起，键都是 "1:2"。
func BuildDMKey(userA, userB uint64) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	return fmt.Sprintf("%d:%d", userA, userB)
}
