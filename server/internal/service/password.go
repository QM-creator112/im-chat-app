// Package service 是业务服务层。
//
// 职责：业务规则、事务编排、校验。
// 不负责：HTTP 协议解析（handler 的事）、SQL 细节（store 的事）。
package service

import (
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// 密码与用户名长度限制。
//
// ⚠️ MaxPasswordLen 之所以是 64 而不是 72：
// bcrypt 的算法上限是 **72 字节**，超过部分会被**静默截断**。
// 与其让用户以为很长的密码更安全（实际后半段无效），
// 不如在注册时直接拦住，并给出明确提示。
const (
	MinPasswordLen = 8
	MaxPasswordLen = 64

	MinUsernameLen = 4
	MaxUsernameLen = 32

	MaxNicknameLen = 32
)

// HashPassword 用 bcrypt 生成密码哈希。
//
// bcrypt 的三个关键特性：
//  1. **故意慢**：一次哈希约 50–100ms，暴力破解代价极高
//  2. **自带随机盐**：盐被编码在结果字符串里，不需要额外存字段
//  3. **结果长度固定 60 字符**：所以数据库用 VARCHAR(60) 就够
//
// 对比一下**绝不该用**的做法：
//
//	md5(password)              // MD5 是为"快"设计的，GPU 每秒能算几十亿次
//	sha256(password + "salt")  // 加固定盐只能防彩虹表，防不住暴力破解
//
// 密码哈希的核心指标是「**故意慢**」，不是「快」。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword 比对明文与哈希。
//
// bcrypt 会**自动从哈希里取出盐和 cost**，用同样参数重新计算再比对，
// 所以不需要我们把盐单独存起来。
//
// 返回 bool 而不是 error：调用方只关心「对不对」。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidatePassword 校验密码强度：8–64 位，且同时含字母与数字。
func ValidatePassword(plain string) error {
	// 用 len() 而不是 RuneCount：bcrypt 的限制是**字节数**，
	// 而一个中文字符占 3 字节，用字节数判断才与 bcrypt 的语义一致。
	if len(plain) < MinPasswordLen || len(plain) > MaxPasswordLen {
		return NewValidationError("密码长度需在 %d-%d 之间", MinPasswordLen, MaxPasswordLen)
	}
	var hasLetter, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return NewValidationError("密码必须同时包含字母和数字")
	}
	return nil
}

// ValidateUsername 校验用户名。
//
// 规则：4–32 位，首字符为英文字母，其余为英文字母/数字/下划线。
// 禁止其它字符（含中文）是为了避免：
//   - 用户名里带空格导致 URL 与命令行歧义
//   - 中文用户名在不同终端下宽度不一致，日志对齐困难
//
// ⚠️ 注意：**不能只用 unicode.IsLetter 判断** ——
// 中文字符同样满足 IsLetter，所以必须额外用 r > unicode.MaxASCII
// 把非 ASCII 字符挡掉。这是一个真实踩过的坑。
func ValidateUsername(name string) error {
	if len(name) < MinUsernameLen || len(name) > MaxUsernameLen {
		return NewValidationError("用户名长度需在 %d-%d 之间", MinUsernameLen, MaxUsernameLen)
	}
	for i, r := range name {
		isASCIILetter := unicode.IsLetter(r) && r <= unicode.MaxASCII
		isASCIIDigit := unicode.IsDigit(r) && r <= unicode.MaxASCII

		if i == 0 {
			if !isASCIILetter {
				return NewValidationError("用户名必须以英文字母开头")
			}
			continue
		}
		if !isASCIILetter && !isASCIIDigit && r != '_' {
			return NewValidationError("用户名只能包含英文字母、数字和下划线")
		}
	}
	return nil
}

// ValidateNickname 校验昵称（允许中文与 emoji，只限长度）。
func ValidateNickname(nickname string) error {
	// 昵称用 rune 计数：用户理解的「32 个字符」是 32 个汉字/emoji，不是 32 字节
	if len([]rune(nickname)) > MaxNicknameLen {
		return NewValidationError("昵称最长 %d 个字符", MaxNicknameLen)
	}
	return nil
}
