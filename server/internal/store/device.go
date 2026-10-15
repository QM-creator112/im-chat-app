package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// DeviceStore 登录设备（refresh token）数据访问。
type DeviceStore struct {
	db *sql.DB
}

// NewDeviceStore 构造 DeviceStore。
func NewDeviceStore(db *sql.DB) *DeviceStore {
	return &DeviceStore{db: db}
}

// Create 保存一条设备记录。
//
// ⚠️ 传入的必须是 refresh token 的 **SHA-256 哈希**，不是明文。
// 这样即使数据库泄露，攻击者也拿不到能直接使用的 refresh token。
func (s *DeviceStore) Create(ctx context.Context, userID uint64, tokenHash, userAgent string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_devices (user_id, refresh_token_hash, user_agent, expires_at)
		 VALUES (?, ?, ?, ?)`,
		userID, tokenHash, truncate(userAgent, 255), expiresAt)
	return err
}

// GetByTokenHash 按 token 哈希查设备。
func (s *DeviceStore) GetByTokenHash(ctx context.Context, tokenHash string) (*model.Device, error) {
	var d model.Device
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, refresh_token_hash, user_agent, expires_at, created_at
		   FROM user_devices WHERE refresh_token_hash = ?`, tokenHash).
		Scan(&d.ID, &d.UserID, &d.RefreshTokenHash, &d.UserAgent, &d.ExpiresAt, &d.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

// DeleteByTokenHash 删除一条设备记录（退出登录）。
//
// 返回是否真的删掉了一行 —— 用于判断这个 refresh token 是否有效。
func (s *DeviceStore) DeleteByTokenHash(ctx context.Context, tokenHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM user_devices WHERE refresh_token_hash = ?`, tokenHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteExpired 清理过期设备记录，返回删除行数。
//
// 可以挂在定时任务里，避免表无限增长。
func (s *DeviceStore) DeleteExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM user_devices WHERE expires_at < NOW(3)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// truncate 按字节截断字符串（避免超出列宽报错）。
//
// 注意按 rune 截断，否则会把一个中文字符切成两半产生乱码。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
