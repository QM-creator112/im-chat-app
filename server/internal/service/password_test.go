package service

import (
	"errors"
	"strings"
	"testing"
)

// TestHashAndCheckPassword 验证哈希长度与校验行为。
//
// 保护的不变量：bcrypt 的结果必须是 60 字符（数据库列宽依赖这个假设）。
func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("Test1234")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}

	// 数据库里 password_hash 是 VARCHAR(60)，长度不符会直接插入失败
	if len(hash) != 60 {
		t.Fatalf("bcrypt 哈希应为 60 字符，实际 %d", len(hash))
	}
	if !CheckPassword(hash, "Test1234") {
		t.Error("正确密码应通过校验")
	}
	if CheckPassword(hash, "WrongPass1") {
		t.Error("错误密码不应通过校验")
	}
	// 大小写敏感
	if CheckPassword(hash, "test1234") {
		t.Error("密码应区分大小写")
	}
}

// TestHashIsSalted 验证每次哈希都带不同的随机盐。
//
// 若这条失败，说明盐是固定的 —— 彩虹表就能批量破解。
func TestHashIsSalted(t *testing.T) {
	h1, err := HashPassword("Test1234")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("Test1234")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("同一密码两次哈希结果相同，说明没有随机盐")
	}
	// 但两个哈希都必须能验证通过
	if !CheckPassword(h1, "Test1234") || !CheckPassword(h2, "Test1234") {
		t.Error("带盐的哈希应仍能正确校验")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name string
		pwd  string
		ok   bool
		note string
	}{
		{"正常", "Test1234", true, ""},
		{"太短", "Ab1", false, "长度不足 8"},
		{"边界-8位", "Abcd1234", true, "刚好 8 位应通过"},
		{"边界-64位", strings.Repeat("a", 63) + "1", true, "刚好 64 位应通过"},
		{"太长-65位", strings.Repeat("a", 64) + "1", false, "超过 64"},
		{"纯字母", "abcdefgh", false, "必须含数字"},
		{"纯数字", "12345678", false, "必须含字母"},
	}
	for _, c := range cases {
		err := ValidatePassword(c.pwd)
		if c.ok && err != nil {
			t.Errorf("[%s] 期望通过，实际报错: %v (%s)", c.name, err, c.note)
		}
		if !c.ok && err == nil {
			t.Errorf("[%s] 期望报错，实际通过 (%s)", c.name, c.note)
		}
		// 校验错误必须是 ValidationError 类型 —— handler 依赖这个类型来决定
		// 错误信息能不能安全返回给客户端
		if err != nil && !IsValidationError(err) {
			t.Errorf("[%s] 校验失败应返回 ValidationError，实际 %T", c.name, err)
		}
	}
}

// TestValidateUsernameRejectsNonASCII 是针对一个**真实 bug 的回归测试**。
//
// 背景：最初实现用 unicode.IsLetter 判断字符是否合法，
// 但**中文字符同样满足 IsLetter**，导致「张abc」这种用户名能通过校验。
//
// 修法是额外加 r > unicode.MaxASCII 的限制。
// 这个测试保证该 bug 不会回来。
func TestValidateUsernameRejectsNonASCII(t *testing.T) {
	bad := []struct {
		name string
		user string
		why  string
	}{
		{"中文开头", "张abc", "首字符必须是英文字母"},
		{"中文结尾", "abc张", "中间/结尾不允许非 ASCII"},
		{"全中文", "张三李四", "不允许中文"},
		{"全角数字", "abc１２３", "全角数字不是 ASCII 数字"},
		{"含空格", "abc def", "不允许空格"},
		{"含连字符", "abc-def", "只允许下划线"},
		{"含点", "abc.def", "不允许点"},
		{"emoji", "abc😀", "不允许 emoji"},
		{"太短", "abc", "长度不足 4"},
		{"数字开头", "1abcd", "首字符必须是字母"},
		{"下划线开头", "_abcd", "首字符必须是字母"},
	}
	for _, c := range bad {
		if err := ValidateUsername(c.user); err == nil {
			t.Errorf("[%s] 用户名 %q 不该通过校验（%s）", c.name, c.user, c.why)
		}
	}
}

func TestValidateUsernameAccepts(t *testing.T) {
	good := []string{"test001", "Abcd1234", "a_b_c", "user_2027", "TestUser_01"}
	for _, u := range good {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("用户名 %q 应当通过校验，实际报错: %v", u, err)
		}
	}
}

// TestValidateNicknameAllowsUnicode 昵称允许中文与 emoji，只限长度。
func TestValidateNicknameAllowsUnicode(t *testing.T) {
	if err := ValidateNickname("邱漫"); err != nil {
		t.Errorf("中文昵称应通过: %v", err)
	}
	if err := ValidateNickname("😀测试"); err != nil {
		t.Errorf("emoji 昵称应通过: %v", err)
	}
	// 32 个汉字应通过，33 个应失败（按 rune 计数，不是按字节）
	if err := ValidateNickname(strings.Repeat("中", 32)); err != nil {
		t.Errorf("32 个汉字应通过: %v", err)
	}
	if err := ValidateNickname(strings.Repeat("中", 33)); err == nil {
		t.Error("33 个汉字应被拒绝")
	}
}

// TestIsValidationErrorRejectsOtherErrors 确认内部错误不会被误判为校验错误。
//
// 这条很重要：如果内部错误被当成校验错误，handler 就会把
// SQL / 路径 / 连接串原样返回给客户端 —— 那是信息泄露。
func TestIsValidationErrorRejectsOtherErrors(t *testing.T) {
	if IsValidationError(nil) {
		t.Error("nil 不应被判定为校验错误")
	}
	if IsValidationError(errors.New("用户名: table users doesn't exist")) {
		t.Error("普通 error 不该被判定为校验错误，即使文案以『用户名』开头")
	}
	if IsValidationError(ErrBadCredential) {
		t.Error("ErrBadCredential 不是校验错误")
	}
	if !IsValidationError(NewValidationError("测试 %d", 1)) {
		t.Error("ValidationError 应被识别")
	}
}
