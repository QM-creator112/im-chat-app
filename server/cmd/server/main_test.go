package main

import (
	"strings"
	"testing"
)

// TestRedactQueryHidesToken 是一条**安全回归测试**。
//
// 背景：WebSocket 握手用 ?token=<accessToken>，而最初的请求日志
// 直接把 RawQuery 原样打进日志。真实日志里出现过：
//
//	query="token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1aWQiOjEs..."
//
// 日志一旦泄露（分享给同事、上传到日志平台、贴进 issue），
// 就等于令牌泄露。所以必须在进日志前脱敏。
func TestRedactQueryHidesToken(t *testing.T) {
	cases := []struct{ name, in, want, mustNotContain string }{
		{
			name:           "WebSocket 的 token",
			in:             "token=eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOjF9.abc",
			want:           "token=%2A%2A%2A",
			mustNotContain: "eyJhbGciOiJIUzI1NiJ9",
		},
		{
			name:           "token + 其他参数",
			in:             "foo=1&token=secret-value&bar=2",
			want:           "bar=2&foo=1&token=%2A%2A%2A",
			mustNotContain: "secret-value",
		},
		{
			name:           "大小写混写也要脱敏",
			in:             "ToKeN=abcdef",
			want:           "ToKeN=%2A%2A%2A",
			mustNotContain: "abcdef",
		},
		{
			name:           "refresh_token",
			in:             "refresh_token=abcd1234",
			want:           "refresh_token=%2A%2A%2A",
			mustNotContain: "abcd1234",
		},
		{
			name:           "password",
			in:             "password=hunter2",
			want:           "password=%2A%2A%2A",
			mustNotContain: "hunter2",
		},
	}

	for _, c := range cases {
		got := redactQuery(c.in)
		if got != c.want {
			t.Errorf("[%s] redactQuery(%q)\n  得到 %q\n  期望 %q", c.name, c.in, got, c.want)
		}
		if c.mustNotContain != "" && strings.Contains(got, c.mustNotContain) {
			t.Errorf("[%s] 脱敏结果里仍含敏感值 %q: %q", c.name, c.mustNotContain, got)
		}
	}
}

// TestRedactQueryKeepsNonSensitiveQuery 验证普通参数原样保留。
//
// 这条防止「为了安全把所有 query 都抹掉」的过度处理 ——
// 那样排查问题时（比如 keyword 搜索）会完全失去线索。
func TestRedactQueryKeepsNonSensitiveQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"keyword=abc", "keyword=abc"},
		{"limit=20&cursor=123", "limit=20&cursor=123"},
		{"keyword=%E4%B8%AD%E6%96%87", "keyword=%E4%B8%AD%E6%96%87"}, // 中文，已是 URL 编码
	}
	for _, c := range cases {
		if got := redactQuery(c.in); got != c.want {
			t.Errorf("redactQuery(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestRedactQueryMalformed 验证畸形 query 不会泄露也不会 panic。
func TestRedactQueryMalformed(t *testing.T) {
	for _, in := range []string{"token=%zz", "%%%", "token"} {
		got := redactQuery(in)
		// 解析失败时整体返回 *** 是最保守的选择
		if got == "" {
			continue // 空输入返回空，没问题
		}
		if strings.Contains(got, "token=") && !strings.Contains(got, "%2A%2A%2A") && got != "token" {
			t.Errorf("畸形输入 %q 的脱敏结果可疑: %q", in, got)
		}
	}
}
