package service

import (
	"context"
	"errors"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// 会话相关错误。
var (
	ErrConversationNotFound = errors.New("会话不存在")
	ErrNotMember            = errors.New("不是该会话成员")
)

// ConversationService 会话业务。
type ConversationService struct {
	convs *store.ConversationStore
	users *store.UserStore
}

// NewConversationService 构造 ConversationService。
func NewConversationService(convs *store.ConversationStore, users *store.UserStore) *ConversationService {
	return &ConversationService{convs: convs, users: users}
}

// EnsureDM 取得或创建与目标用户的单聊会话。
func (s *ConversationService) EnsureDM(ctx context.Context, userID, peerID uint64) (uint64, error) {
	if userID == peerID {
		return 0, NewValidationError("不能和自己创建会话")
	}
	// 先确认对方存在，否则会建出一条指向不存在用户的会话
	if _, err := s.users.GetByID(ctx, peerID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, NewValidationError("目标用户不存在")
		}
		return 0, err
	}
	return s.convs.GetOrCreateDM(ctx, userID, peerID)
}

// CreateGroup 创建群聊。
func (s *ConversationService) CreateGroup(ctx context.Context, ownerID uint64, title string, memberIDs []uint64) (uint64, error) {
	if err := ValidateNickname(title); err != nil { // 复用长度校验
		return 0, NewValidationError("群名长度不合法（最长 32 字）")
	}
	if len(memberIDs) == 0 {
		return 0, NewValidationError("群聊至少需要一个成员")
	}
	if len(memberIDs) > 200 {
		return 0, NewValidationError("群成员数量超出上限")
	}
	// 校验每个成员都存在，避免建出一个有"幽灵成员"的群
	for _, m := range memberIDs {
		if _, err := s.users.GetByID(ctx, m); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return 0, NewValidationError("群成员中存在无效用户")
			}
			return 0, err
		}
	}
	return s.convs.CreateGroup(ctx, ownerID, title, memberIDs)
}

// List 返回用户的会话列表。
func (s *ConversationService) List(ctx context.Context, userID uint64, limit int) ([]model.ConversationListItem, error) {
	return s.convs.ListForUser(ctx, userID, limit)
}

// Members 返回会话成员（调用者必须是成员）。
func (s *ConversationService) Members(ctx context.Context, convID, userID uint64) ([]model.Member, error) {
	ok, err := s.convs.IsMember(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	return s.convs.ListMembers(ctx, convID)
}

// IsMember 暴露成员判断，供消息服务复用。
func (s *ConversationService) IsMember(ctx context.Context, convID, userID uint64) (bool, error) {
	return s.convs.IsMember(ctx, convID, userID)
}
