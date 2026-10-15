package service

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testSecret = "unit-test-secret-at-least-32-bytes-long!!"

func TestSignAndParseToken(t *testing.T) {
	token, err := SignAccessToken(testSecret, 1001, time.Minute)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if token == "" {
		t.Fatal("签发的 token 为空")
	}

	claims, err := ParseAccessToken(testSecret, token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if claims.UserID != 1001 {
		t.Errorf("期望 uid=1001，实际 %d", claims.UserID)
	}
	if claims.Issuer != TokenIssuer {
		t.Errorf("期望 issuer=%s，实际 %s", TokenIssuer, claims.Issuer)
	}
	if claims.ExpiresAt == nil {
		t.Error("token 必须带 exp")
	}
}

// TestParseTokenWrongSecret 验证用错误密钥签发的 token 无法通过校验。
func TestParseTokenWrongSecret(t *testing.T) {
	token, err := SignAccessToken("secret-a-at-least-32-bytes-long-aaaa", 1001, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAccessToken("secret-b-at-least-32-bytes-long-bbbb", token); err == nil {
		t.Error("用错误的密钥解析应当失败")
	}
}

// TestExpiredTokenRejected 验证过期令牌被拒绝，且返回**可区分**的错误。
//
// 为什么要区分：客户端收到「过期」应当自动用 refresh token 换新的（用户无感），
// 收到「无效」则应当跳登录页。两者体验完全不同。
func TestExpiredTokenRejected(t *testing.T) {
	token, err := SignAccessToken(testSecret, 1001, -time.Minute) // 已过期
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseAccessToken(testSecret, token)
	if err == nil {
		t.Fatal("过期令牌应当被拒绝")
	}
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("应返回 ErrTokenExpired，实际: %v", err)
	}
}

// TestAlgorithmConfusionRejected 验证不会接受非 HMAC 算法的 token。
//
// 这是 JWT 最经典的漏洞（算法混淆攻击）：
// 如果验签函数直接 `return []byte(secret), nil` 而不检查算法，
// 攻击者可以把 header 的 alg 改成 "none"，构造出不需签名的 token。
//
// 这里手工构造一个 alg=none 的 token，确认解析器会拒绝。
func TestAlgorithmConfusionRejected(t *testing.T) {
	// 手工拼一个 alg=none 的 token：header.payload.（空签名）
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"uid":1,"iss":"` + TokenIssuer + `","exp":` +
			strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`))
	tokenStr := header + "." + payload + "."

	if _, err := ParseAccessToken(testSecret, tokenStr); err == nil {
		t.Error("alg=none 的 token 必须被拒绝（算法混淆漏洞）")
	}
}

// TestHashRefreshToken 验证 refresh token 的哈希是确定性的 64 位十六进制。
func TestHashRefreshToken(t *testing.T) {
	plain, hash, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if plain == "" || hash == "" {
		t.Fatal("返回值不应为空")
	}
	if len(hash) != 64 {
		t.Errorf("SHA-256 的十六进制表示应为 64 字符，实际 %d", len(hash))
	}
	// 同输入必须同输出（否则查库时找不到记录）
	if HashRefreshToken(plain) != hash {
		t.Error("同一输入的哈希必须一致")
	}
	// 明文和哈希不能相同
	if plain == hash {
		t.Error("哈希不应等于明文")
	}
}

// TestRefreshTokenIsRandom 验证每次生成的 refresh token 都不同。
func TestRefreshTokenIsRandom(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		plain, _, err := NewRefreshToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[plain] {
			t.Fatalf("第 %d 次生成的 refresh token 与之前重复", i)
		}
		seen[plain] = true
		if strings.ContainsAny(plain, "+/=") {
			t.Errorf("应当使用 RawURLEncoding（不含 + / =），实际: %s", plain)
		}
	}
}

// TestParseTokenGarbage 验证垃圾输入不会 panic。
func TestParseTokenGarbage(t *testing.T) {
	for _, s := range []string{"", "abc", "a.b.c", "....", strings.Repeat("x", 5000)} {
		if _, err := ParseAccessToken(testSecret, s); err == nil {
			t.Errorf("垃圾输入 %q 不该解析成功", s)
		}
	}
}
