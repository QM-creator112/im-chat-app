package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// UserStore 用户数据访问。
type UserStore struct {
	db *sql.DB
}

// NewUserStore 构造 UserStore。
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// CreateUser 插入用户并返回自增 ID。
//
// 注意：**这里不做用户名查重**。
// 正确性由 users.uk_username 唯一索引保证 ——
// 应用层的查重只是为了给用户友好提示，不是并发安全的。
// 并发两个请求同时通过查重、然后一个撞唯一索引，是必须处理的正常路径。
func (s *UserStore) CreateUser(ctx context.Context, username, passwordHash, nickname string) (uint64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, nickname) VALUES (?, ?, ?)`,
		username, passwordHash, nickname)
	if err != nil {
		return 0, err // 撞唯一索引时原样返回，由上层用 IsDuplicateKey 判断
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("读取自增 ID 失败: %w", err)
	}
	return uint64(id), nil
}

// GetByUsername 按用户名查用户（登录用）。
func (s *UserStore) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, nickname, avatar_url, created_at, updated_at
		   FROM users WHERE username = ?`, username)
	return scanUser(row)
}

// GetByID 按 ID 查用户。
func (s *UserStore) GetByID(ctx context.Context, id uint64) (*model.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, nickname, avatar_url, created_at, updated_at
		   FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// UsernameExists 判断用户名是否已存在。
//
// ⚠️ 仅供「友好提示」使用，**不构成并发安全保证**。
// 真正的唯一性由数据库唯一索引兜底。
func (s *UserStore) UsernameExists(ctx context.Context, username string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM users WHERE username = ?`, username).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SearchByKeyword 按用户名/昵称模糊搜索（加好友用）。
func (s *UserStore) SearchByKeyword(ctx context.Context, keyword string, limit int) ([]model.User, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	// 转义 LIKE 通配符，避免用户输入 % 或 _ 时意外匹配全部
	kw := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(keyword)
	like := "%" + kw + "%"

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, password_hash, nickname, avatar_url, created_at, updated_at
		   FROM users
		  WHERE username LIKE ? ESCAPE '\\' OR nickname LIKE ? ESCAPE '\\'
		  ORDER BY id ASC
		  LIMIT ?`, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Nickname,
			&u.AvatarURL, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// scanUser 把一行扫描成 User，并把 sql.ErrNoRows 转成 store.ErrNotFound。
func scanUser(row *sql.Row) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Nickname,
		&u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}
