package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// 群管理相关错误。
var (
	ErrNotGroup         = NewValidationError("只有群聊支持该操作")
	ErrOwnerCannotLeave = NewValidationError("群主不能退群，请先转让群主或解散群")
)

// 群成员数量上限。超过这个数写扩散的代价会明显上升，需要换读扩散。
const maxGroupMembers = 200

// AddMembers 把若干用户拉进群。
//
// 三层校验，顺序不能乱：
//  1. 会话存在 且 是群聊
//  2. 操作者是这个群的成员（否则任何人猜到 convId 就能往群里塞人）
//  3. 每个 userId 都是真实用户（否则会建出指向不存在用户的"幽灵成员"）
func (s *ConversationService) AddMembers(ctx context.Context, convID, operatorID uint64, userIDs []uint64) (int64, error) {
	if len(userIDs) == 0 {
		return 0, NewValidationError("userIds 不能为空")
	}

	conv, err := s.convs.GetByID(ctx, convID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, ErrConversationNotFound
		}
		return 0, err
	}
	if conv.Type != model.ConversationTypeGroup {
		return 0, ErrNotGroup
	}

	isMember, err := s.convs.IsMember(ctx, convID, operatorID)
	if err != nil {
		return 0, err
	}
	if !isMember {
		return 0, ErrNotMember
	}

	// 先确认所有目标用户都存在 —— 有一个无效就整体拒绝，
	// 避免"拉了 3 个人成功 2 个"的半成功状态让调用方难以处理。
	for _, uid := range userIDs {
		if _, err := s.users.GetByID(ctx, uid); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return 0, NewValidationError("存在无效的用户 ID")
			}
			return 0, err
		}
	}

	// 容量检查要在插入前做
	current, err := s.convs.CountMembers(ctx, convID)
	if err != nil {
		return 0, err
	}
	if current+len(userIDs) > maxGroupMembers {
		return 0, NewValidationError("群成员上限 %d 人", maxGroupMembers)
	}

	added, err := s.convs.AddMembers(ctx, convID, userIDs)
	if err != nil {
		return 0, err
	}

	slog.Info("群成员新增",
		"conversation_id", convID, "operator_id", operatorID,
		"requested", len(userIDs), "added", added)
	return added, nil
}

// Leave 退出群聊。
//
// 业务规则（写进契约 docs/api.md）：
//   - 只有群聊能退
//   - **群主不能退**：否则群变成无主状态，没人能再管理
//   - 幂等：本来就不在群里也算成功
//   - 历史消息保留：退群只影响"以后能不能收"，不删除已发生的记录
func (s *ConversationService) Leave(ctx context.Context, convID, userID uint64) error {
	conv, err := s.convs.GetByID(ctx, convID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrConversationNotFound
		}
		return err
	}
	if conv.Type != model.ConversationTypeGroup {
		return ErrNotGroup
	}
	if conv.OwnerID == userID {
		return ErrOwnerCannotLeave
	}

	removed, err := s.convs.RemoveMember(ctx, convID, userID)
	if err != nil {
		return err
	}
	if !removed {
		// 本来就不在群里 —— 幂等，不当作错误
		slog.Info("退群：用户本不在群内，视为成功", "conversation_id", convID, "user_id", userID)
		return nil
	}

	slog.Info("用户退群", "conversation_id", convID, "user_id", userID)
	return nil
}
