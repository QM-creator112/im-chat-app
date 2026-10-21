package store

import (
	"context"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// ListRecent 返回会话里最近的 limit 条消息（按 seq 升序返回）。
//
// 用途：把最近若干条对话喂给 AI 侧车做总结/回复。
//
// 实现说明：先按 seq DESC 取 N 条再反转，而不是 ORDER BY seq ASC LIMIT N ——
// 后者取到的是**最早的** N 条，对"最近对话"毫无用处。
// 这个顺序问题很容易写反，所以单独说明。
func (s *MessageStore) ListRecent(ctx context.Context, convID uint64, limit int) ([]model.Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, messageSelect+`
		  WHERE m.conversation_id = ?
		  ORDER BY m.seq DESC
		  LIMIT ?`, convID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	desc := make([]model.Message, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		desc = append(desc, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 反转为升序：AI 需要按时间顺序读上下文
	out := make([]model.Message, len(desc))
	for i, m := range desc {
		out[len(desc)-1-i] = m
	}
	return out, nil
}
