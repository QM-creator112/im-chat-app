package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/config"
	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// 本文件是 Sprint 3 的核心验收测试。
//
// 它们需要真实 MySQL（因为要验证 FOR UPDATE 的并发语义、唯一索引兜底、
// 索引实际被用到 —— 这些用 mock 测不出来，mock 只能测我们自己写的逻辑）。
//
// 没有数据库时**明确跳过**而不是伪装通过：
//   t.Skip 会在 go test -v 里显示 SKIP 及原因，不会静默算"测试通过"。
//   CI 里后续会加 MySQL service 容器来真正跑起来（第 5 篇）。

// openTestDB 连接测试库。连不上就跳过。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("跳过：读取配置失败（%v）", err)
	}
	db, err := Open(cfg.DB.DSN())
	if err != nil {
		t.Skipf("跳过：连不上 MySQL（%v）", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// testFixture 是测试用的最小数据集。
type testFixture struct {
	UserA, UserB uint64
	ConvID       uint64
}

// newFixture 创建两个随机用户名用户 + 一条单聊会话，并注册清理。
func newFixture(t *testing.T, db *sql.DB) *testFixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	mkUser := func(tag string) uint64 {
		res, err := db.ExecContext(ctx,
			`INSERT INTO users (username, password_hash, nickname) VALUES (?, ?, ?)`,
			fmt.Sprintf("it_%d_%s", suffix, tag),
			strings.Repeat("x", 60), // 假哈希，测试不验证密码
			"测试"+tag)
		if err != nil {
			t.Fatalf("创建测试用户失败: %v", err)
		}
		id, _ := res.LastInsertId()
		return uint64(id)
	}

	f := &testFixture{UserA: mkUser("a"), UserB: mkUser("b")}

	convStore := NewConversationStore(db)
	convID, err := convStore.GetOrCreateDM(ctx, f.UserA, f.UserB)
	if err != nil {
		t.Fatalf("创建单聊会话失败: %v", err)
	}
	f.ConvID = convID

	// 清理顺序：消息 → 成员 → 会话 → 用户
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`, f.ConvID)
		_, _ = db.ExecContext(ctx, `DELETE FROM conversation_members WHERE conversation_id = ?`, f.ConvID)
		_, _ = db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, f.ConvID)
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id IN (?, ?)`, f.UserA, f.UserB)
	})
	return f
}

// countMessages 统计会话里的消息条数。
func countMessages(t *testing.T, db *sql.DB, convID uint64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, convID).Scan(&n); err != nil {
		t.Fatalf("统计消息失败: %v", err)
	}
	return n
}

// TestSendAllocatesSeqFromOne 验证 seq 从 1 开始逐条递增。
func TestSendAllocatesSeqFromOne(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		msg, err := ms.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       f.UserA,
			ClientMsgID:    fmt.Sprintf("seq-test-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("第 %d 条", i),
		})
		if err != nil {
			t.Fatalf("第 %d 条发送失败: %v", i, err)
		}
		if msg.Seq != uint64(i) {
			t.Errorf("第 %d 条消息的 seq 应为 %d，实际 %d", i, i, msg.Seq)
		}
		if msg.CreatedAtMs == 0 {
			t.Error("CreatedAtMs 未填充（前端拿不到时间戳）")
		}
	}
}

// TestSendConcurrentSeqUnique 是 Sprint 3 最重要的一条压力测试。
//
// 20 个协程各发 50 条（共 1000 条）到**同一个会话**，验证：
//   - 全部成功（没有因 seq 冲突而失败）
//   - 库里正好 1000 条
//   - seq 互不重复，且覆盖 1..1000（无空洞 —— 因为全部成功的事务都推进了分配器）
//
// 这条测试直接对应「FOR UPDATE 把同一会话的写串行化」这个设计是否成立。
// 如果去掉 FOR UPDATE，它必然失败（并发读到同一个 next_seq）。
func TestSendConcurrentSeqUnique(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	ctx := context.Background()

	const goroutines = 20
	const perGoroutine = 50
	const total = goroutines * perGoroutine

	var wg sync.WaitGroup
	errCh := make(chan error, total)
	seqCh := make(chan uint64, total)

	start := time.Now()
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				msg, err := ms.Send(ctx, SendInput{
					ConversationID: f.ConvID,
					SenderID:       f.UserA,
					ClientMsgID:    fmt.Sprintf("conc-%d-%d-%d", g, i, start.UnixNano()),
					ContentType:    model.ContentTypeText,
					Content:        fmt.Sprintf("g%d-%d", g, i),
				})
				if err != nil {
					errCh <- err
					return
				}
				seqCh <- msg.Seq
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	close(seqCh)
	elapsed := time.Since(start)

	for err := range errCh {
		t.Fatalf("并发发送出错: %v", err)
	}

	// 校验唯一性
	seen := make(map[uint64]struct{}, total)
	for s := range seqCh {
		if _, dup := seen[s]; dup {
			t.Fatalf("seq %d 重复分配！说明 FOR UPDATE 没生效", s)
		}
		seen[s] = struct{}{}
	}
	if len(seen) != total {
		t.Errorf("seq 数量应为 %d，实际 %d", total, len(seen))
	}

	// 校验无空洞：全部事务都成功，所以 1..total 应全部出现
	for i := uint64(1); i <= uint64(total); i++ {
		if _, ok := seen[i]; !ok {
			t.Fatalf("seq %d 缺失（出现空洞）—— 在全部成功的情况下不该有空洞", i)
		}
	}

	// 校验落库条数
	if n := countMessages(t, db, f.ConvID); n != total {
		t.Errorf("库里消息数应为 %d，实际 %d", total, n)
	}

	t.Logf("并发 %d 协程 × %d 条 = %d 条，耗时 %v（约 %.2f 条/毫秒）",
		goroutines, perGoroutine, total, elapsed.Round(time.Millisecond),
		float64(total)/float64(elapsed.Milliseconds()))
}

// TestSendDuplicateClientMsgIDRejected 验证数据库唯一索引确实拦住了重复。
//
// 注意 store 层不做幂等"返回旧记录"，那是 service 的职责；
// store 层的职责是**保证物理上不会插进第二条**。
func TestSendDuplicateClientMsgIDRejected(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	ctx := context.Background()

	in := SendInput{
		ConversationID: f.ConvID,
		SenderID:       f.UserA,
		ClientMsgID:    fmt.Sprintf("dup-%d", time.Now().UnixNano()),
		ContentType:    model.ContentTypeText,
		Content:        "只应入库一条",
	}

	if _, err := ms.Send(ctx, in); err != nil {
		t.Fatalf("第一次发送应成功: %v", err)
	}

	_, err := ms.Send(ctx, in)
	if err == nil {
		t.Fatal("第二次发送应当报唯一索引冲突")
	}
	if idx := DuplicateIndex(err); idx != "uk_sender_client" {
		t.Errorf("冲突索引应为 uk_sender_client，实际 %q（错误: %v）", idx, err)
	}

	if n := countMessages(t, db, f.ConvID); n != 1 {
		t.Errorf("库里应只有 1 条消息，实际 %d", n)
	}
}

// TestSyncSince 验证增量补拉。
func TestSyncSince(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	ctx := context.Background()

	const n = 10
	for i := 1; i <= n; i++ {
		if _, err := ms.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       f.UserA,
			ClientMsgID:    fmt.Sprintf("sync-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("msg %d", i),
		}); err != nil {
			t.Fatalf("发送第 %d 条失败: %v", i, err)
		}
	}

	// 从 seq=5 之后开始拉，应拿到 6..10 共 5 条
	items, hasMore, err := ms.SyncSince(ctx, f.ConvID, 5, 100)
	if err != nil {
		t.Fatalf("SyncSince 失败: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("应返回 5 条，实际 %d", len(items))
	}
	if hasMore {
		t.Error("不足 limit，hasMore 应为 false")
	}
	for i, m := range items {
		want := uint64(6 + i)
		if m.Seq != want {
			t.Errorf("第 %d 条的 seq 应为 %d，实际 %d", i, want, m.Seq)
		}
	}

	// 从 0 开始拉（simulate 全量补拉）
	all, _, err := ms.SyncSince(ctx, f.ConvID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != n {
		t.Errorf("从 0 开始应拿到 %d 条，实际 %d", n, len(all))
	}

	// 拉最新之后（无新消息）应返回空
	none, _, err := ms.SyncSince(ctx, f.ConvID, n, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("没有新消息时应返回 0 条，实际 %d", len(none))
	}
}

// TestSyncSinceHasMore 验证超过 limit 时 hasMore 为 true（对应 hasGap）。
func TestSyncSinceHasMore(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	ctx := context.Background()

	for i := 1; i <= 8; i++ {
		if _, err := ms.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       f.UserA,
			ClientMsgID:    fmt.Sprintf("more-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("msg %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// limit=5，共 8 条 → 应返回 5 条且 hasMore=true
	items, hasMore, err := ms.SyncSince(ctx, f.ConvID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Errorf("应返回 5 条，实际 %d", len(items))
	}
	if !hasMore {
		t.Error("还有 3 条没返回，hasMore 应为 true")
	}
}

// TestListBeforePagination 验证向上翻页无重复无遗漏。
func TestListBeforePagination(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	ctx := context.Background()

	const total = 25
	for i := 1; i <= total; i++ {
		if _, err := ms.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       f.UserA,
			ClientMsgID:    fmt.Sprintf("page-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("msg %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 第 1 页：最新 10 条（16..25）——25 条里最新的 10 条
	page1, hasMore, err := ms.ListBefore(ctx, f.ConvID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 10 {
		t.Fatalf("第 1 页应有 10 条，实际 %d", len(page1))
	}
	if !hasMore {
		t.Error("后面还有历史，hasMore 应为 true")
	}
	if page1[0].Seq != 16 || page1[9].Seq != 25 {
		t.Errorf("第 1 页范围应为 16..25，实际 %d..%d", page1[0].Seq, page1[9].Seq)
	}
	// 升序返回
	for i := 1; i < len(page1); i++ {
		if page1[i].Seq <= page1[i-1].Seq {
			t.Fatalf("第 1 页未按 seq 升序: %d 在 %d 之后", page1[i].Seq, page1[i-1].Seq)
		}
	}

	// 第 2 页：比 16 更早的 10 条（6..15）
	page2, hasMore2, err := ms.ListBefore(ctx, f.ConvID, page1[0].Seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 10 {
		t.Fatalf("第 2 页应有 10 条，实际 %d", len(page2))
	}
	if page2[9].Seq != 15 {
		t.Errorf("第 2 页最后一条应为 15，实际 %d", page2[9].Seq)
	}
	if !hasMore2 {
		t.Error("还有 5 条，hasMore 应为 true")
	}

	// 第 3 页：1..5，且没有更多
	page3, hasMore3, err := ms.ListBefore(ctx, f.ConvID, page2[0].Seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 5 {
		t.Fatalf("第 3 页应有 5 条，实际 %d", len(page3))
	}
	if hasMore3 {
		t.Error("已到最早，hasMore 应为 false")
	}

	// 关键校验：三页拼起来正好是 1..25，无重复无遗漏
	all := append(append(append([]model.Message{}, page3...), page2...), page1...)
	if len(all) != total {
		t.Fatalf("三页合计应为 %d 条，实际 %d", total, len(all))
	}
	for i, m := range all {
		want := uint64(i + 1)
		if m.Seq != want {
			t.Fatalf("第 %d 条 seq 应为 %d，实际 %d（有重复或遗漏）", i, want, m.Seq)
		}
	}
}

// TestUpdateLastReadNoRegression 验证已读位置只前进不回退。
//
// 这个行为防止：网络乱序时「读到 10」先到、「读到 5」后到，
// 导致界面上的「已读」变回「未读」。
func TestUpdateLastReadNoRegression(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	cs := NewConversationStore(db)
	ctx := context.Background()

	// 前进到 10
	if err := cs.UpdateLastRead(ctx, f.ConvID, f.UserA, 10); err != nil {
		t.Fatalf("推进已读失败: %v", err)
	}
	var got uint64
	if err := db.QueryRow(`SELECT last_read_seq FROM conversation_members
	                        WHERE conversation_id = ? AND user_id = ?`,
		f.ConvID, f.UserA).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("已读应为 10，实际 %d", got)
	}

	// 用一个更小的值尝试回退 —— 必须无效
	if err := cs.UpdateLastRead(ctx, f.ConvID, f.UserA, 5); err != nil {
		t.Fatalf("回退调用不该报错: %v", err)
	}
	if err := db.QueryRow(`SELECT last_read_seq FROM conversation_members
	                        WHERE conversation_id = ? AND user_id = ?`,
		f.ConvID, f.UserA).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Errorf("已读不应回退，期望仍是 10，实际 %d", got)
	}

	// 前进到 12 应生效
	if err := cs.UpdateLastRead(ctx, f.ConvID, f.UserA, 12); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT last_read_seq FROM conversation_members
	                        WHERE conversation_id = ? AND user_id = ?`,
		f.ConvID, f.UserA).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Errorf("已读应前进到 12，实际 %d", got)
	}
}

// TestUpdateLastReadRejectsNonMember 验证非成员不能上报已读。
func TestUpdateLastReadRejectsNonMember(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	cs := NewConversationStore(db)
	ctx := context.Background()

	// 造一个不属于该会话的用户
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, nickname) VALUES (?, ?, ?)`,
		fmt.Sprintf("it_%d_outsider", time.Now().UnixNano()), strings.Repeat("x", 60), "路人")
	if err != nil {
		t.Fatal(err)
	}
	outsiderID, _ := res.LastInsertId()
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, outsiderID)
	})

	err = cs.UpdateLastRead(ctx, f.ConvID, uint64(outsiderID), 5)
	if err == nil {
		t.Fatal("非成员上报已读应当被拒绝")
	}
	if !isErrNotMember(err) {
		t.Errorf("应返回 ErrNotMember，实际: %v", err)
	}
}

func isErrNotMember(err error) bool {
	return err != nil && err.Error() == ErrNotMember.Error()
}

// TestListForUserUnreadCount 验证会话列表的未读数计算。
func TestListForUserUnreadCount(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	ms := NewMessageStore(db)
	cs := NewConversationStore(db)
	ctx := context.Background()

	// A 发 5 条，B 读 2 条 → B 应有 3 条未读
	for i := 1; i <= 5; i++ {
		if _, err := ms.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       f.UserA,
			ClientMsgID:    fmt.Sprintf("unread-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("msg %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cs.UpdateLastRead(ctx, f.ConvID, f.UserB, 2); err != nil {
		t.Fatal(err)
	}

	list, err := cs.ListForUser(ctx, f.UserB, 50)
	if err != nil {
		t.Fatalf("查会话列表失败: %v", err)
	}
	var target *model.ConversationListItem
	for i := range list {
		if list[i].ID == f.ConvID {
			target = &list[i]
			break
		}
	}
	if target == nil {
		t.Fatal("会话列表里找不到测试会话")
	}
	if target.UnreadCount != 3 {
		t.Errorf("未读数应为 3，实际 %d", target.UnreadCount)
	}
	if target.LatestSeq != 5 {
		t.Errorf("latestSeq 应为 5，实际 %d", target.LatestSeq)
	}
	if target.LastMsg != "msg 5" {
		t.Errorf("最后一条消息应为 'msg 5'，实际 %q", target.LastMsg)
	}
	if target.LastMsgAt == 0 {
		t.Error("LastMsgAt 未填充")
	}
	// 单聊的 title 应解析成对方昵称
	if target.Title != "测试a" {
		t.Errorf("单聊标题应为对方昵称 '测试a'，实际 %q", target.Title)
	}
}

// TestGetOrCreateDMIsIdempotent 验证同一对用户重复建单聊只得到同一个会话。
func TestGetOrCreateDMIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	f := newFixture(t, db)
	cs := NewConversationStore(db)
	ctx := context.Background()

	// 再来 5 次，都应该是同一个 id
	for i := 0; i < 5; i++ {
		id, err := cs.GetOrCreateDM(ctx, f.UserA, f.UserB)
		if err != nil {
			t.Fatalf("第 %d 次获取失败: %v", i, err)
		}
		if id != f.ConvID {
			t.Fatalf("第 %d 次拿到了不同的会话 id: %d（应为 %d）", i, id, f.ConvID)
		}
	}

	// 反向顺序也必须命中同一个（dm_key 排序保证）
	id, err := cs.GetOrCreateDM(ctx, f.UserB, f.UserA)
	if err != nil {
		t.Fatal(err)
	}
	if id != f.ConvID {
		t.Errorf("反向调用应命中同一会话，实际 %d", id)
	}

	// 库里该 dm_key 只能有一条
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE dm_key = ?`,
		model.BuildDMKey(f.UserA, f.UserB)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("单聊会话应只有 1 条，实际 %d", n)
	}
}
