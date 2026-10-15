package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenIssuer 签发者标识，用于校验 token 确实由本服务签发。
const TokenIssuer = "im-chat-app"

// 令牌相关错误。
//
// 分开定义 ErrTokenExpired 是为了让客户端能区分：
//   - 无效 token  → 直接跳登录页
//   - 过期 token  → 用 refresh token 换新的（用户无感）
var (
	ErrTokenInvalid = errors.New("令牌无效")
	ErrTokenExpired = errors.New("令牌已过期")
)

// Claims 是 access token 的载荷。
type Claims struct {
	UserID uint64 `json:"uid"`
	jwt.RegisteredClaims
}

// SignAccessToken 签发访问令牌。
//
// 用 HS256（对称签名）而不是 RS256（非对称）的原因：
// 只有我们自己的服务需要验证这个 token，不需要让第三方验签，
// 对称签名更快也更简单（不用管理密钥对）。
func SignAccessToken(secret string, userID uint64, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    TokenIssuer,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("签发令牌失败: %w", err)
	}
	return signed, nil
}

// ParseAccessToken 校验并解析访问令牌。
//
// ⚠️ 下面那个算法校验**绝对不能省**。
//
// 这是 JWT 最经典的漏洞：如果实现写成
//
//	func(t *jwt.Token) (any, error) { return []byte(secret), nil }
//
// 而不检查签名算法，攻击者可以把 header 里的 alg 改成 "none"，
// 或者从 HS256 换成 RS256（用公钥当 HMAC 密钥），
// 构造一个**不需要私钥就能通过验证**的 token。
//
// 面试被问「JWT 有什么安全坑」时，这一条是标准答案。
func ParseAccessToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("%w: 非预期的签名算法 %v",
					ErrTokenInvalid, t.Header["alg"])
			}
			return []byte(secret), nil
		},
		jwt.WithIssuer(TokenIssuer),  // 校验签发者
		jwt.WithExpirationRequired(), // 必须有 exp，不允许「永不过期」的 token
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

// NewRefreshToken 生成随机 refresh token。
//
// 返回值：
//   - plain：给客户端的**明文**（客户端自己保存）
//   - hash ：存进数据库的 **SHA-256 哈希**
//
// 为什么数据库存哈希而不是明文：
// 这样即使数据库泄露，攻击者也拿不到能直接使用的 refresh token ——
// 与"密码存哈希"是同一个道理。
func NewRefreshToken() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("生成随机令牌失败: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, HashRefreshToken(plain), nil
}

// HashRefreshToken 计算 refresh token 的 SHA-256 哈希（十六进制，64 字符）。
//
// 这里用 SHA-256 而不用 bcrypt 是有意的：
// refresh token 本身就是 32 字节的高熵随机串，不存在"弱口令"问题，
// 不需要"故意慢"。而每个请求都要查一次，用 bcrypt 会明显拖慢。
func HashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
