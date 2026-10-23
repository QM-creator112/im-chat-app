package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// AI 在会话里发消息（Sprint 6）。
//
// 核心原则：**聊天是主链路，AI 是增强功能。增强功能的失败不能拖垮主链路。**
// 具体体现：
//  1. AI 超时/报错**不返回错误**给用户，而是把失败变成一条系统提示消息
//  2. @AI 触发是异步的，不阻塞发消息的响应
//  3. AI 侧车不可达时，聊天完全正常，只是 @AI 会得到"暂时不可用"

const (
	// aiAskContextMessages 喂给 AI 的最近消息条数。
	//
	// 20 条：既能让模型理解上下文，又把 token 成本控制在很低的范围。
	// 完整的会话总结应该按"最近 N 条"或"最后一天"来界定，而不是全量 ——
	// 全量既贵又超出模型窗口。
	aiAskContextMessages = 20

	// aiFallbackText AI 失败时写进会话的降级文案。
	//
	// 为什么要落成一条消息而不是返回 HTTP 错误：
	// 用户在聊天界面里发起了 @AI，只有在**聊天里**才能看到结果。
	// 如果返回 500，前端只能弹一个 toast，用户不知道 AI 到底说了什么。
	// 落一条系统消息，体验上"AI 回复了你，但它在忙"。
	aiFallbackText = "AI 助手暂时不可用，请稍后再试。"
)

// AIKind AI 请求的类型。
type AIKind string

const (
	AIKindSummary AIKind = "summary" // 总结最近对话
	AIKindReply   AIKind = "reply"   // 生成一条回复建议
)

// errAINotConfigured 未配置 AI 侧车。
var errAINotConfigured = errors.New("AI 侧车未配置")

// parseAICommand 判断一条消息是否触发了 AI，并返回请求类型。
//
// 约定：消息**以 @AI 开头**（大小写不敏感）即触发。
//   - "@AI 总结一下"     → 总结
//   - "@AI 帮我回一句"   → 回复建议
//   - 单独一个 "@AI"      → 默认总结
//
// 为什么用前缀而不是"内容里包含 @"：
// 前缀规则明确、好实现、也不会误伤"我昨天@AI了一下"这种正文。
func parseAICommand(content string) (AIKind, bool) {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) < 3 {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "@ai") {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[3:])
	if strings.Contains(rest, "回复") || strings.Contains(rest, "reply") {
		return AIKindReply, true
	}
	// 默认按总结处理（"总结"/"概括"/空都走这里）
	return AIKindSummary, true
}

// triggerAIAsync 异步触发 AI，不阻塞调用方。
//
// 用 context.Background() 而不是请求的 ctx：
// 请求结束后 ctx 会被取消，如果 AI 还在等模型响应就会被一起掐断。
// 这里给一个**略大于 aiTimeout** 的独立超时，保证有界。
func (s *MessageService) triggerAIAsync(convID, requesterID uint64, kind AIKind) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiTimeout+5*time.Second)
		defer cancel()

		if _, err := s.AskAI(ctx, convID, requesterID, kind); err != nil {
			// 这里的错误已经是"连降级消息都写不进去"级别（比如成员被删了），
			// 记录日志即可，绝不能 panic 掉服务
			slog.Warn("AI 自动触发失败", "conversation_id", convID, "err", err)
		}
	}()
}

// AskAI 让 AI 在会话里发一条消息。
//
// 返回值语义：
//   - 第一个返回值是**AI 那条消息**（成功或降级提示都是消息）
//   - 第二个 error 只在"连消息都写不进去"时非空（比如不是成员、数据库故障）
//
// 也就是说：**AI 调用失败不算 error**，它会变成一条降级消息。
func (s *MessageService) AskAI(ctx context.Context, convID, requesterID uint64, kind AIKind) (*model.Message, error) {
	ok, err := s.convs.IsMember(ctx, convID, requesterID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}

	text, callErr := s.callAI(ctx, convID, kind)
	if callErr != nil {
		// ---- 降级：把失败变成一条友好的系统消息 ----
		slog.Warn("AI 调用失败，降级为系统提示",
			"conversation_id", convID, "kind", kind, "err", callErr)
		text = aiFallbackText
	}

	return s.persistAIMessage(ctx, convID, requesterID, text, callErr == nil)
}

// callAI 拼上下文并调用侧车。
func (s *MessageService) callAI(ctx context.Context, convID uint64, kind AIKind) (string, error) {
	if s.ai == nil {
		return "", errAINotConfigured
	}

	recent, err := s.msgs.ListRecent(ctx, convID, aiAskContextMessages)
	if err != nil {
		return "", fmt.Errorf("读取最近消息失败: %w", err)
	}
	if len(recent) == 0 {
		return "", errors.New("会话里没有消息可供 AI 参考")
	}

	// 拼成 "昵称: 内容" —— 让模型能区分谁说的
	lines := make([]string, 0, len(recent))
	for _, m := range recent {
		name := m.SenderName
		if name == "" {
			name = fmt.Sprintf("用户%d", m.SenderID)
		}
		lines = append(lines, fmt.Sprintf("%s: %s", name, m.Content))
	}

	var res *AIResult
	if kind == AIKindReply {
		res, err = s.ai.Reply(ctx, lines)
	} else {
		res, err = s.ai.Summarize(ctx, lines)
	}
	if err != nil {
		return "", err
	}

	slog.Info("AI 调用成功", "conversation_id", convID, "kind", kind, "mode", res.Mode)
	return res.Text, nil
}

// persistAIMessage 把 AI 内容落库并投递。
//
// 关于"谁发的"：sender_id 记在**发起请求的用户**名下，
// contentType 用系统消息（5）—— 这样：
//   - 消息可追溯到"谁问的"
//   - 前端可以按 contentType 渲染成"AI 卡片"而不是普通气泡
//   - 关键：**它是一条真正的消息**，有自己的 seq，所有人按序收到
func (s *MessageService) persistAIMessage(
	ctx context.Context, convID, requesterID uint64, text string, ok bool,
) (*model.Message, error) {
	// 服务端生成幂等键：AI 消息不是客户端发的，没有 client_msg_id，
	// 但 uk_sender_client 唯一索引要求它有值，所以生成一个 UUIDv7。
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("生成消息 ID 失败: %w", err)
	}

	// 前缀用 **[AI] 文字标记**，与侧车的 [MOCK] 同一套写法：
	//   · 不用 emoji —— 用图形符号当状态标记是"生成式界面"的典型痕迹，
	//     而且 emoji 在不同系统字形不一，做对比度检查时也不可控
	//   · 成功时标注这是 AI 总结；失败时原文已含原因（如"暂时不可用"），
	//     只加统一前缀，两种结果都能一眼认出是 AI 产生的
	prefix := "[AI] "
	if ok {
		prefix = "[AI] 总结："
	}
	content := prefix + text

	msg, err := s.msgs.Send(ctx, store.SendInput{
		ConversationID: convID,
		SenderID:       requesterID,
		ClientMsgID:    id.String(),
		ContentType:    model.ContentTypeSystem,
		Content:        content,
	})
	if err != nil {
		return nil, fmt.Errorf("写入 AI 消息失败: %w", err)
	}

	// ⚠️ 用 deliverToAll 而不是 deliver：
	// AI 消息是服务端生成的，请求者本地**没有**乐观显示这条，
	// 如果沿用"不推给发送者"的规则，发起 @AI 的人就永远看不到回复。
	s.deliverToAll(ctx, msg)
	return msg, nil
}
