package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/cache"
	"github.com/QM-creator112/im-chat-app/server/internal/model"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// 消息相关错误。
var (
	ErrMessageProcessing = errors.New("消息正在处理中，请稍后重试")
	ErrEmptyContent      = errors.New("消息内容不能为空")
)

// 内容长度上限。
const (
	maxContentLen = 8000 // 单条文本上限（按 rune 计）
	// idemTTL 幂等结果的缓存时长。
	//
	// 24 小时：覆盖用户"发失败→隔天再重试"的场景，又不至于无限增长。
	// 即使缓存过期也不会出错 —— 还有数据库唯一索引兜底。
	idemTTL = 24 * time.Hour
	// idemPlaceholderTTL 占位的存活时间。必须远大于一次落库耗时（毫秒级），
	// 但要足够小，让"进程崩溃后残留占位"能自愈 —— 5 分钟是折中。
	idemPlaceholderTTL = 5 * time.Minute
)

// Pusher 负责把消息推给在线用户。
//
// 定义成接口而不是直接依赖 ws.Hub，有两个原因：
//  1. 打破循环依赖：ws 包已经 import service（用 ParseAccessToken），
//     如果 service 再 import ws 就成环了。
//  2. 可测试：单测里传一个假实现即可，不需要真实 WebSocket。
//
// 由 ws.Hub 隐式实现（Go 的结构化类型满足）。
type Pusher interface {
	// PushMessage 把新消息推给指定用户，返回成功投递的连接数（0 = 离线）。
	PushMessage(recipientID uint64, msg *model.Message) int
	// PushRead 把已读回执推给指定用户。
	PushRead(recipientID, convID, readerID, readSeq uint64) int
}

// SendMessageInput 发送消息入参。
type SendMessageInput struct {
	SenderID       uint64
	ConversationID uint64
	ClientMsgID    string
	ContentType    uint8
	Content        string
}

// MessageService 消息业务。
type MessageService struct {
	msgs   *store.MessageStore
	convs  *store.ConversationStore
	idem   *cache.IdempotencyStore
	pusher Pusher
	// ai AI 侧车客户端，可以为 nil（未配置 AI 时降级为"AI 暂时不可用"的系统消息）
	ai *AIClient
}

// NewMessageService 构造 MessageService。
//
// pusher 可以为 nil（不投递，仅落库）；ai 可以为 nil（AI 功能降级）。
func NewMessageService(msgs *store.MessageStore, convs *store.ConversationStore,
	idem *cache.IdempotencyStore, pusher Pusher, ai *AIClient) *MessageService {
	return &MessageService{msgs: msgs, convs: convs, idem: idem, pusher: pusher, ai: ai}
}

// Send 是消息发送的唯一入口：校验 → 幂等 → 落库（分配 seq）→ 投递。
//
// 幂等分三层，任何一层单独失效都不会产生重复消息：
//
//	第 ① 层 Redis：快路径，绝大多数重试在这里被挡住，不碰数据库
//	第 ② 层 MySQL：uk_sender_client 唯一索引兜底。Redis 整个挂掉也不会重复
//	第 ③ 层 客户端：按 seq 去重，防止推送重复导致界面出现两条
//
// ❗最容易写错的一步：落库失败时必须释放 Redis 占位。
// 不释放的话，那个 key 会活到 TTL 到期，客户端在这段时间内**每次重试
// 都拿到「处理中」**，用户看到的现象是「消息永远发不出去，而且不报错」。
func (s *MessageService) Send(ctx context.Context, in SendMessageInput) (*model.Message, error) {
	// ---- 前置校验 ----
	if err := validateContent(in.Content, in.ContentType); err != nil {
		return nil, err
	}
	if in.ClientMsgID == "" {
		return nil, NewValidationError("缺少 clientMsgId")
	}

	ok, err := s.convs.IsMember(ctx, in.ConversationID, in.SenderID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}

	// ============ 第 ① 层：Redis 快速路径 ============
	acquired, existingSeq, rerr := s.idem.TryLock(ctx, in.SenderID, in.ClientMsgID, idemPlaceholderTTL)
	switch {
	case rerr != nil:
		// Redis 故障不阻断主流程 —— 还有第 ② 层数据库唯一索引兜底。
		// 这是本设计的核心取舍：**Redis 是加速器，不是正确性的依赖**。
		slog.Warn("幂等缓存不可用，降级到数据库唯一索引", "err", rerr)
	case !acquired && existingSeq > 0:
		// 已处理过：把上次的结果原样返回（幂等成功）
		if msg, err := s.msgs.GetByClientMsgID(ctx, in.SenderID, in.ClientMsgID); err == nil {
			slog.Info("幂等命中（Redis）", "user_id", in.SenderID,
				"client_msg_id", in.ClientMsgID, "seq", msg.Seq)
			return msg, nil
		}
		// 缓存说有、数据库却没有（缓存脏了）→ 清掉，继续走正常流程
		_ = s.idem.Release(ctx, in.SenderID, in.ClientMsgID)
	case !acquired && existingSeq == cache.Placeholder:
		// 另一个请求正在处理同一条消息 → 让客户端稍后重试
		return nil, ErrMessageProcessing
	}

	// ============ 第 ② 层：落库（唯一索引兜底） ============
	msg, err := s.msgs.Send(ctx, store.SendInput{
		ConversationID: in.ConversationID,
		SenderID:       in.SenderID,
		ClientMsgID:    in.ClientMsgID,
		ContentType:    in.ContentType,
		Content:        in.Content,
	})
	if err != nil {
		idx := store.DuplicateIndex(err)
		switch idx {
		case "uk_sender_client":
			// 唯一索引命中 = 这条消息已经存在（多半是 Redis 缓存丢了）。
			// 这不是错误，把已有的那条返回即可。
			slog.Info("幂等命中（数据库唯一索引）", "user_id", in.SenderID,
				"client_msg_id", in.ClientMsgID)
			existing, err2 := s.msgs.GetByClientMsgID(ctx, in.SenderID, in.ClientMsgID)
			if err2 != nil {
				return nil, err2
			}
			_ = s.idem.SetResult(ctx, in.SenderID, in.ClientMsgID, existing.Seq, idemTTL)
			return existing, nil

		case "uk_conv_seq":
			// seq 重复 = 并发控制失效，是**真 bug**，绝不能当成幂等成功吞掉
			slog.Error("⚠ seq 重复，并发控制失效", "conversation_id", in.ConversationID, "err", err)
			_ = s.idem.Release(ctx, in.SenderID, in.ClientMsgID)
			return nil, err

		default:
			// 其它失败（连接断开、超时、约束等）→ 必须释放占位
			_ = s.idem.Release(ctx, in.SenderID, in.ClientMsgID)
			return nil, err
		}
	}

	// ============ 第 ③ 层准备：把结果写回缓存 ============
	// 写失败不影响正确性（下次重试会走数据库唯一索引），只影响性能
	if err := s.idem.SetResult(ctx, in.SenderID, in.ClientMsgID, msg.Seq, idemTTL); err != nil {
		slog.Warn("写幂等结果失败（不影响正确性）", "err", err)
	}

	// ---- 投递（至少一次；离线则跳过，等对方上线按游标补拉）----
	s.deliver(ctx, msg)

	// ---- @AI 触发（Sprint 6）----
	// 必须在**发送成功之后**、且异步执行：
	// 大模型可能几秒才回，同步做会让"发消息"这个动作卡住，
	// 而发消息是主链路，AI 只是增强功能。
	if kind, ok := parseAICommand(in.Content); ok {
		s.triggerAIAsync(in.ConversationID, in.SenderID, kind)
	}

	return msg, nil
}

// deliver 把消息推给会话内除发送者外的其他成员。
//
// 为什么同步调用而不开 goroutine：
//   - Hub.SendToUser 本身是非阻塞的（投递到有界 channel），开销极小
//   - 同步能保证**同一会话的推送顺序**与 seq 顺序一致
//     （开 goroutine 的话两个并发 Send 的推送顺序可能颠倒）
func (s *MessageService) deliver(ctx context.Context, msg *model.Message) {
	s.deliverExcept(ctx, msg, msg.SenderID)
}

// deliverToAll 推给会话内**所有**成员，包括 sender 自己。
//
// 为什么需要它（这是 Sprint 6 里真实踩到的坑）：
//
//	AI 回复是**服务端生成**的消息，请求者并没有在本地乐观显示它。
//	如果沿用普通消息的"不推给发送者"规则，发起 @AI 的人**永远收不到 AI 的回复**，
//	除非手动刷新/重新同步 —— 体验上就是"我 @ 了 AI，它没理我"。
//
// 所以 AI 消息必须推给所有人（含发起者）。
func (s *MessageService) deliverToAll(ctx context.Context, msg *model.Message) {
	s.deliverExcept(ctx, msg, 0) // 0 不是合法用户 ID，因此谁都不排除
}

// deliverExcept 推给除 excludeID 外的所有成员。
func (s *MessageService) deliverExcept(ctx context.Context, msg *model.Message, excludeID uint64) {
	if s.pusher == nil {
		return
	}
	members, err := s.convs.ListMemberIDs(ctx, msg.ConversationID)
	if err != nil {
		slog.Error("投递失败：查成员列表出错", "conversation_id", msg.ConversationID, "err", err)
		return
	}

	for _, uid := range members {
		if uid == excludeID {
			continue // 普通消息不推给发送者：他本地已经乐观显示了一条
		}
		if n := s.pusher.PushMessage(uid, msg); n == 0 {
			// 对方不在线 —— 这不是错误。
			// 消息已经落库，他上线后会通过 /sync 按游标补拉。
			slog.Info("接收端离线，跳过在线投递",
				"conversation_id", msg.ConversationID, "seq", msg.Seq, "peer_id", uid)
		}
	}
}

// History 向前翻页拉历史消息。
func (s *MessageService) History(ctx context.Context, convID, userID, beforeSeq uint64, limit int) ([]model.Message, bool, error) {
	ok, err := s.convs.IsMember(ctx, convID, userID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, ErrNotMember
	}
	return s.msgs.ListBefore(ctx, convID, beforeSeq, limit)
}

// SyncResult 增量补拉结果。
type SyncResult struct {
	Items     []model.Message
	LatestSeq uint64
	HasGap    bool
}

// Sync 断线重连后的增量补拉。
//
// hasGap 的语义（客户端据此决定要不要降级）：
//   - false：返回的 items + latestSeq 足够把本地补齐
//   - true ：增量太多被截断了（或已超出保留窗口），客户端应改为
//     「按会话重新拉最近 N 条」，而不是继续增量拉
//
// 注意：**不能用「seq 是否连续」判断有没有丢失**。
// seq 是单调递增的，不是连续的（事务回滚会留下空洞 —— 见 store.Send 的说明）。
// 判断缺失的唯一可靠方式就是调这个接口拿服务端真实列表比对。
func (s *MessageService) Sync(ctx context.Context, convID, userID, sinceSeq uint64) (*SyncResult, error) {
	ok, err := s.convs.IsMember(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}

	const limit = 500
	items, hasMore, err := s.msgs.SyncSince(ctx, convID, sinceSeq, limit)
	if err != nil {
		return nil, err
	}

	// latestSeq 用于推进客户端游标。
	// 即使 items 为空也要返回真实的当前值，否则客户端游标永远不动。
	latest, err := s.convs.MaxSeq(ctx, convID)
	if err != nil {
		return nil, err
	}

	return &SyncResult{Items: items, LatestSeq: latest, HasGap: hasMore}, nil
}

// MarkRead 推进已读位置，并把回执推给其他成员。
func (s *MessageService) MarkRead(ctx context.Context, convID, userID, readSeq uint64) error {
	if readSeq == 0 {
		return NewValidationError("readSeq 必须大于 0")
	}
	if err := s.convs.UpdateLastRead(ctx, convID, userID, readSeq); err != nil {
		if errors.Is(err, store.ErrNotMember) {
			return ErrNotMember
		}
		return err
	}

	// 推已读回执给对方（单聊里就是对方；群聊里是其他成员）
	if s.pusher != nil {
		members, err := s.convs.ListMemberIDs(ctx, convID)
		if err != nil {
			slog.Warn("推已读回执失败：查成员出错", "err", err)
			return nil // 已读本身已落库，推送失败不影响正确性
		}
		for _, uid := range members {
			if uid == userID {
				continue
			}
			s.pusher.PushRead(uid, convID, userID, readSeq)
		}
	}
	return nil
}

// validateContent 校验消息内容。
func validateContent(content string, contentType uint8) error {
	switch contentType {
	case model.ContentTypeText:
		if len([]rune(content)) == 0 {
			return ErrEmptyContent
		}
		if len([]rune(content)) > maxContentLen {
			return NewValidationError("消息内容超过 %d 字上限", maxContentLen)
		}
	case model.ContentTypeSystem:
		// 系统消息允许空内容之外的任何文本，同样限长
		if len([]rune(content)) > maxContentLen {
			return NewValidationError("消息内容超过 %d 字上限", maxContentLen)
		}
	default:
		// 图片/文件/语音在 Sprint 5 之后实现，当前先明确拒绝，
		// 而不是静默接受一个前端无法渲染的类型
		return NewValidationError("暂不支持该消息类型")
	}
	return nil
}
