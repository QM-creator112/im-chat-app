package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// 好友相关错误。
var (
	ErrCannotAddSelf = NewValidationError("不能添加自己为好友")
	ErrUserNotFound  = errors.New("目标用户不存在")
)

// FriendService 好友业务。
type FriendService struct {
	friends *store.FriendStore
	users   *store.UserStore
}

// NewFriendService 构造 FriendService。
func NewFriendService(friends *store.FriendStore, users *store.UserStore) *FriendService {
	return &FriendService{friends: friends, users: users}
}

// AddFriend 加好友（双向）。
func (s *FriendService) AddFriend(ctx context.Context, userID, friendID uint64) error {
	if userID == friendID {
		return ErrCannotAddSelf
	}

	// 先确认目标用户存在，否则会写出一条指向不存在用户的关系记录
	if _, err := s.users.GetByID(ctx, friendID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrUserNotFound
		}
		return err
	}

	return s.friends.AddBidirectional(ctx, userID, friendID)
}

// ListFriends 返回好友列表。
func (s *FriendService) ListFriends(ctx context.Context, userID uint64) ([]model.User, error) {
	return s.friends.ListFriends(ctx, userID)
}

// FriendIDs 返回好友 ID 列表。
//
// ⚠️ 这个方法的签名刻意匹配 ws.FriendIDsFunc：
//
//	func(ctx context.Context, userID uint64) []uint64
//
// 因为它会被注入给 WebSocket Hub 用于在线状态广播。
//
// **它故意不返回 error**：在线状态是"锦上添花"的能力，
// 查询失败时返回空列表即可 —— 绝不能因为查不到好友列表就让连接建立失败。
// 这也符合「在线状态广播绝不能拖垮主链路」的设计原则。
func (s *FriendService) FriendIDs(ctx context.Context, userID uint64) []uint64 {
	ids, err := s.friends.ListFriendIDs(ctx, userID)
	if err != nil {
		slog.Warn("查询好友列表失败，跳过在线状态广播", "user_id", userID, "err", err)
		return nil
	}
	return ids
}
