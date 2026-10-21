package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// ErrConversationNotFound 会话不存在。
var ErrConversationNotFound = errors.New("会话不存在")

// messageSelect 所有消息查询共用的 SELECT + FROM 片段。
//
// 用 JOIN users 一次把发送者昵称带出来，而不是查完消息再逐条查用户
// （那就是经典的 N+1 查询：一个 30 条的会话要发 31 次 SQL）。
// 前端渲染消息气泡必须有昵称，推送 payload 里也要带。
const messageSelect = `
SELECT m.id, m.conversation_id, m.seq, m.sender_id, m.client_msg_id,
       m.content_type, m.content, m.created_at, COALESCE(u.nickname, '')
  FROM messages m
  LEFT JOIN users u ON u.id = m.sender_id`

// MessageStore 消息数据访问。
type MessageStore struct {
	db *sql.DB
}

// NewMessageStore 构造 MessageStore。
func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

// SendInput 落库所需的字段。
type SendInput struct {
	ConversationID uint64
	SenderID       uint64
	ClientMsgID    string
	ContentType    uint8
	Content        string
}

// Send 在**同一个事务**里分配 seq 并插入消息。
//
// 这是整个项目最核心的一段代码。四个步骤必须原子：
//
//	① SELECT next_seq FROM conversations WHERE id=? FOR UPDATE   ← 拿行级排他锁
//	② seq = next_seq + 1
//	③ UPDATE conversations SET next_seq = seq
//	④ INSERT INTO messages (...)
//
// 为什么必须原子：如果不加锁，两个并发请求会读到同一个 next_seq，
// 分配出同一个 seq —— 轻则撞 uk_conv_seq 报错，重则（若索引缺失）产生两条
// 同 seq 的消息，顺序彻底乱掉。
//
// ❗FOR UPDATE 的代价要说清楚（面试必问）：
//
//	同一个会话的消息发送被**串行化**了。这是有意的取舍 ——
//	会话内必须有序，而有序就需要一个集中分配器。
//	不同会话之间不互相阻塞（锁的是各自那一行）。
//
//	如果将来单会话写入成为瓶颈，可以改成「Redis 里按会话维护分配器 +
//	预分配区间」来减少数据库锁竞争，但会引入「Redis 挂了序列号可能回退」
//	的问题，需要额外的持久化策略。当前规模下，简单性和正确性更值。
func (s *MessageStore) Send(ctx context.Context, in SendInput) (*model.Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// 提交成功后 Rollback 是安全的 no-op；出错时自动回滚。
	defer func() { _ = tx.Rollback() }()

	// ① 锁住会话行并读出当前序列号
	var seq uint64
	err = tx.QueryRowContext(ctx,
		`SELECT next_seq FROM conversations WHERE id = ? FOR UPDATE`,
		in.ConversationID).Scan(&seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	seq++

	// ② 推进分配器
	if _, err = tx.ExecContext(ctx,
		`UPDATE conversations SET next_seq = ? WHERE id = ?`,
		seq, in.ConversationID); err != nil {
		return nil, err
	}

	// ③ 插入消息
	//
	// 到这里出现两种「撞唯一索引」的情况，必须区分开：
	//   - 撞 uk_sender_client → 说明这条消息**已经存在**（幂等命中），不是错误
	//   - 撞 uk_conv_seq      → 说明代码有 bug（并发没锁住），是真错误
	// 用具体的索引名判断（MySQL 1062 的 message 里带索引名），比笼统地
	// "撞了就算幂等" 精确得多 —— 否则真 bug 会被伪装成幂等成功而长期潜伏。
	res, err := tx.ExecContext(ctx,
		`INSERT INTO messages
		   (conversation_id, seq, sender_id, client_msg_id, content_type, content)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.ConversationID, seq, in.SenderID, in.ClientMsgID, in.ContentType, in.Content)
	if err != nil {
		return nil, err
	}
	lastID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	// ④ 顺手更新会话的 updated_at，让会话列表按最后消息时间倒序正确
	if _, err = tx.ExecContext(ctx,
		`UPDATE conversations SET updated_at = NOW(3) WHERE id = ?`,
		in.ConversationID); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	// 事务内没有回读 created_at（少一次查询），这里补一次读；
	// 代价很小（主键查询），换来的是响应里有准确的毫秒时间戳。
	return s.GetByID(ctx, uint64(lastID))
}

// GetByID 按主键读一条消息。
func (s *MessageStore) GetByID(ctx context.Context, id uint64) (*model.Message, error) {
	row := s.db.QueryRowContext(ctx, messageSelect+` WHERE m.id = ?`, id)
	return scanMessage(row)
}

// GetByClientMsgID 按「发送者 + 客户端消息 ID」查消息。
//
// 这是幂等命中的关键查询：Redis 缓存丢了、或者撞了唯一索引之后，
// 都靠它把「上次已经落库的那一条」找回来返回给客户端。
// 走 uk_sender_client 唯一索引，是精确查找。
func (s *MessageStore) GetByClientMsgID(ctx context.Context, senderID uint64, clientMsgID string) (*model.Message, error) {
	row := s.db.QueryRowContext(ctx, messageSelect+`
		 WHERE m.sender_id = ? AND m.client_msg_id = ?`,
		senderID, clientMsgID)
	return scanMessage(row)
}

// SyncSince 返回会话里 seq > sinceSeq 的消息（按 seq 升序），用于断线补拉。
//
// 返回的第二个值 hasMore 表示「还有更多没返回」—— 调用方据此置 hasGap，
// 让客户端改为「重新拉最近 N 条」而不是继续增量拉。
func (s *MessageStore) SyncSince(ctx context.Context, convID, sinceSeq uint64, limit int) ([]model.Message, bool, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	// 多查一条用来判断"是否还有更多"，比查 COUNT(*) 便宜得多
	rows, err := s.db.QueryContext(ctx, messageSelect+`
		  WHERE m.conversation_id = ? AND m.seq > ?
		  ORDER BY m.seq ASC
		  LIMIT ?`, convID, sinceSeq, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Message, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// ListBefore 向前翻页：返回 seq < beforeSeq 的最近 limit 条消息（按 seq 升序返回）。
//
// 为什么用 seq 而不用 offset 做分页：
//   - offset 在数据插入后会错位（翻页时漏消息或重复）
//   - 深分页要扫过前面所有行，越翻越慢
//
// 用 seq 作游标，每次都走 uk_conv_seq 索引，翻到第 100 页和第一页一样快。
// beforeSeq = 0 表示「从最新开始往前取」。
func (s *MessageStore) ListBefore(ctx context.Context, convID, beforeSeq uint64, limit int) ([]model.Message, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}

	var rows *sql.Rows
	var err error
	if beforeSeq == 0 {
		// beforeSeq=0 表示拿最新的，用倒序 + LIMIT，再反转成升序
		rows, err = s.db.QueryContext(ctx, messageSelect+`
			  WHERE m.conversation_id = ?
			  ORDER BY m.seq DESC
			  LIMIT ?`, convID, limit+1)
	} else {
		rows, err = s.db.QueryContext(ctx, messageSelect+`
			  WHERE m.conversation_id = ? AND m.seq < ?
			  ORDER BY m.seq DESC
			  LIMIT ?`, convID, beforeSeq, limit+1)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	desc := make([]model.Message, 0, limit+1)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		desc = append(desc, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(desc) > limit
	if hasMore {
		desc = desc[:limit]
	}

	// 数据库按 DESC 取出，返回给前端时反转成升序（聊天界面是从上往下读的）
	out := make([]model.Message, len(desc))
	for i, m := range desc {
		out[len(desc)-1-i] = m
	}
	return out, hasMore, nil
}

// scanMessage 把一行扫描成 Message。
//
// 同时填 CreatedAt（time.Time，后端内部用）和 CreatedAtMs（给前端的毫秒戳）。
// 只暴露后者给 JSON，避免前端拿到两种时间格式。
func scanMessage(sc interface {
	Scan(dest ...any) error
}) (*model.Message, error) {
	var m model.Message
	err := sc.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.SenderID, &m.ClientMsgID,
		&m.ContentType, &m.Content, &m.CreatedAt, &m.SenderName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.CreatedAtMs = m.CreatedAt.UnixMilli()
	return &m, nil
}

// duplicateIndexNames 我们关心的唯一索引名。
//
// 顺序有意义：uk_sender_client 是「幂等命中」的合法情况，
// uk_conv_seq 是「代码 bug」的告警信号，两者必须区分开。
var duplicateIndexNames = []string{
	"uk_sender_client", // 幂等：同一发送者同一 client_msg_id 已存在
	"uk_conv_seq",      // 严重：seq 重复 → 并发控制失效
	"uk_dm_key",        // 单聊会话重复
	"uk_username",      // 用户名重复
	"uk_token",         // refresh token 重复
}

// DuplicateIndex 从 MySQL 1062 错误里取出冲突的索引名。
//
// MySQL 的错误信息形如：
//
//	Duplicate entry '1-01JABC...' for key 'messages.uk_sender_client'
//
// 我们据此区分「幂等命中」（uk_sender_client）与「真 bug」（uk_conv_seq）。
// 取不到索引名时返回空串，调用方按保守策略处理。
func DuplicateIndex(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, idx := range duplicateIndexNames {
		if strings.Contains(msg, idx) {
			return idx
		}
	}
	return ""
}
