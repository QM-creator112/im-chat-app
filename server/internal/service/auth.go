package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// 业务错误。
//
// 在 service 层定义错误、在 handler 层映射成错误码 ——
// 这样业务规则不关心 HTTP，HTTP 层也不关心业务细节。
var (
	ErrUsernameTaken   = errors.New("用户名已存在")
	ErrBadCredential   = errors.New("用户名或密码错误")
	ErrBadRefreshToken = errors.New("刷新令牌无效或已过期")
)

// AuthService 账号与鉴权业务。
type AuthService struct {
	users      *store.UserStore
	devices    *store.DeviceStore
	jwtSecret  string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewAuthService 构造 AuthService。
func NewAuthService(users *store.UserStore, devices *store.DeviceStore,
	jwtSecret string, accessTTL, refreshTTL time.Duration) *AuthService {
	return &AuthService{
		users:      users,
		devices:    devices,
		jwtSecret:  jwtSecret,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// RegisterInput 注册入参。
type RegisterInput struct {
	Username string
	Password string
	Nickname string
}

// Register 注册新用户，返回用户 ID。
//
// ⚠️ 关于「查重两次」的设计说明（面试高频问题）：
//
//	第 ① 次查重（UsernameExists）是为了给用户**友好提示**；
//	第 ② 次是 CreateUser 撞唯一索引后把 ErrUsernameTaken 返回 ——
//	这才是**并发安全的正确性保证**。
//
// 因为两个请求可能同时通过第 ① 次检查，只有一个能在 INSERT 时成功。
// 这就是经典的「先检查后执行」（check-then-act）竞态，
// 必须把正确性下沉到数据库唯一索引，不能相信应用层的检查。
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (uint64, error) {
	if err := ValidateUsername(in.Username); err != nil {
		return 0, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return 0, err
	}
	if in.Nickname == "" {
		in.Nickname = in.Username
	}
	if err := ValidateNickname(in.Nickname); err != nil {
		return 0, err
	}

	// ① 友好提示用的查重
	exists, err := s.users.UsernameExists(ctx, in.Username)
	if err != nil {
		return 0, fmt.Errorf("查询用户名失败: %w", err)
	}
	if exists {
		return 0, ErrUsernameTaken
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return 0, err
	}

	// ② 真正的正确性保证
	id, err := s.users.CreateUser(ctx, in.Username, hash, in.Nickname)
	if err != nil {
		if store.IsDuplicateKey(err) {
			return 0, ErrUsernameTaken
		}
		return 0, fmt.Errorf("创建用户失败: %w", err)
	}
	return id, nil
}

// LoginResult 登录结果。
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	User         *model.User
}

// Login 校验账号密码并签发双令牌。
func (s *AuthService) Login(ctx context.Context, username, password, userAgent string) (*LoginResult, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// ⚠️ 用户不存在时也返回 ErrBadCredential，**不要**返回"用户不存在"。
			// 否则攻击者可以用登录接口枚举出哪些用户名已注册。
			return nil, ErrBadCredential
		}
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	if !CheckPassword(u.PasswordHash, password) {
		return nil, ErrBadCredential
	}

	accessToken, err := SignAccessToken(s.jwtSecret, u.ID, s.accessTTL)
	if err != nil {
		return nil, err
	}

	plain, hash, err := NewRefreshToken()
	if err != nil {
		return nil, err
	}
	if err := s.devices.Create(ctx, u.ID, hash, userAgent, time.Now().Add(s.refreshTTL)); err != nil {
		return nil, fmt.Errorf("保存登录设备失败: %w", err)
	}

	return &LoginResult{AccessToken: accessToken, RefreshToken: plain, User: u}, nil
}

// Refresh 用 refresh token 换新的 access token。
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (string, error) {
	if refreshToken == "" {
		return "", ErrBadRefreshToken
	}

	hash := HashRefreshToken(refreshToken)
	d, err := s.devices.GetByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrBadRefreshToken
		}
		return "", fmt.Errorf("查询设备失败: %w", err)
	}

	if time.Now().After(d.ExpiresAt) {
		// 顺手清理过期记录，避免表无限增长
		if _, delErr := s.devices.DeleteByTokenHash(ctx, hash); delErr != nil {
			slog.Warn("清理过期设备记录失败", "err", delErr)
		}
		return "", ErrBadRefreshToken
	}

	return SignAccessToken(s.jwtSecret, d.UserID, s.accessTTL)
}

// Logout 退出登录（删除该 refresh token 对应的设备记录）。
//
// 注意：即使记录不存在也返回 nil ——
// 退出登录应当是**幂等**的，重复登出不该报错。
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	if _, err := s.devices.DeleteByTokenHash(ctx, HashRefreshToken(refreshToken)); err != nil {
		return fmt.Errorf("删除设备记录失败: %w", err)
	}
	return nil
}

// GetUser 读取用户资料。
func (s *AuthService) GetUser(ctx context.Context, id uint64) (*model.User, error) {
	return s.users.GetByID(ctx, id)
}

// SearchUsers 按关键词搜索用户。
func (s *AuthService) SearchUsers(ctx context.Context, keyword string, limit int) ([]model.User, error) {
	if keyword == "" {
		return []model.User{}, nil
	}
	return s.users.SearchByKeyword(ctx, keyword, limit)
}
