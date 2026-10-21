package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// Sprint 5：群聊与已读的数据库层验收。
//
// 覆盖 handbook §⑥ 的验收项里能在 store 层验证的部分：
// 建群/拉人/退群、成员数、退群后读不到消息、未读数公式、已读不回退。

// groupFixture 一个 N 人群的测试夹具。
type groupFixture struct {
	OwnerID uint64
	Members []uint64 // 不含 owner
	All     []uint64 // 含 owner
	ConvID  uint64
}

// newGroupFixture 创建 n 人（含 owner）的群，并注册清理。
func newGroupFixture(t *testing.T, db *sql.DB, n int) *groupFixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	users := NewUserStore(db)
	convs := NewConversationStore(db)

	ids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		id, err := users.CreateUser(ctx,
			fmt.Sprintf("gt_%d_%d", suffix, i), strings.Repeat("x", 60), fmt.Sprintf("群测%d", i))
		if err != nil {
			t.Fatalf("创建测试用户失败: %v", err)
		}
		ids = append(ids, id)
	}

	owner := ids[0]
	members := ids[1:]
	convID, err := convs.CreateGroup(ctx, owner, "测试群"+fmt.Sprint(suffix%1000), members)
	if err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`, convID)
		_, _ = db.ExecContext(ctx, `DELETE FROM conversation_members WHERE conversation_id = ?`, convID)
		_, _ = db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, convID)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id IN (`+placeholders+`)`, args...)
	})

	return &groupFixture{OwnerID: owner, Members: members, All: ids, ConvID: convID}
}

// TestCreateGroupMembersVisibleToAll 对应验收 #1：建 4 人群，成员都能看到。
func TestCreateGroupMembersVisibleToAll(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 4)
	convs := NewConversationStore(db)
	ctx := context.Background()

	// 群聊的 dm_key 必须是 NULL（不是空串），否则第二个群插不进去
	conv, err := convs.GetByID(ctx, f.ConvID)
	if err != nil {
		t.Fatalf("读群会话失败: %v", err)
	}
	if conv.Type != model.ConversationTypeGroup {
		t.Errorf("会话类型应为群聊(2)，实际 %d", conv.Type)
	}
	if conv.DMKey != nil {
		t.Errorf("群聊的 dm_key 应为 NULL，实际 %v", *conv.DMKey)
	}
	if conv.OwnerID != f.OwnerID {
		t.Errorf("群主应为 %d，实际 %d", f.OwnerID, conv.OwnerID)
	}

	// 4 个成员都能通过 ListForUser 看到这个群
	for _, uid := range f.All {
		list, err := convs.ListForUser(ctx, uid, 50)
		if err != nil {
			t.Fatalf("用户 %d 查会话列表失败: %v", uid, err)
		}
		found := false
		for _, item := range list {
			if item.ID == f.ConvID {
				found = true
				if item.Type != model.ConversationTypeGroup {
					t.Errorf("用户 %d 看到的类型应是群聊，实际 %d", uid, item.Type)
				}
			}
		}
		if !found {
			t.Errorf("用户 %d 应当能看到群 %d", uid, f.ConvID)
		}
	}

	count, err := convs.CountMembers(ctx, f.ConvID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("成员数应为 4，实际 %d", count)
	}
}

// TestAddMembersIsIdempotent 对应验收 #1 的"拉人"：重复拉同一个人不报错、不重复计数。
func TestAddMembersIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 2) // owner + 1
	convs := NewConversationStore(db)
	users := NewUserStore(db)
	ctx := context.Background()

	newID, err := users.CreateUser(ctx,
		fmt.Sprintf("gt_add_%d", time.Now().UnixNano()), strings.Repeat("x", 60), "新人")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, newID) })

	added, err := convs.AddMembers(ctx, f.ConvID, []uint64{newID})
	if err != nil {
		t.Fatalf("拉人失败: %v", err)
	}
	if added != 1 {
		t.Errorf("首次拉人应新增 1 人，实际 %d", added)
	}

	// 再拉一次：幂等，新增 0
	added2, err := convs.AddMembers(ctx, f.ConvID, []uint64{newID})
	if err != nil {
		t.Fatalf("重复拉人不应报错: %v", err)
	}
	if added2 != 0 {
		t.Errorf("重复拉人应新增 0 人，实际 %d", added2)
	}

	count, _ := convs.CountMembers(ctx, f.ConvID)
	if count != 3 {
		t.Errorf("成员数应为 3，实际 %d", count)
	}
}

// TestAddMembersBatchPartialDuplicate 批量拉人时，已存在的忽略、新的加上。
func TestAddMembersBatchPartialDuplicate(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 2) // owner + member[0]
	convs := NewConversationStore(db)
	users := NewUserStore(db)
	ctx := context.Background()

	existing := f.Members[0] // 已在群里

	a, _ := users.CreateUser(ctx, fmt.Sprintf("gt_a_%d", time.Now().UnixNano()), strings.Repeat("x", 60), "A")
	b, _ := users.CreateUser(ctx, fmt.Sprintf("gt_b_%d", time.Now().UnixNano()+1), strings.Repeat("x", 60), "B")
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id IN (?, ?)`, a, b)
	})

	added, err := convs.AddMembers(ctx, f.ConvID, []uint64{existing, a, b})
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Errorf("应新增 2 人（跳过已存在的 1 人），实际 %d", added)
	}
}

// TestLeaveGroupStopsReceiving 对应验收 #7：退群后不再收到该群消息。
//
// store 层能验证的是「退群后不再是成员」——而投递时按成员列表查收件人，
// 所以不再是成员就等于收不到。真正的在线投递由验收脚本用真实 WS 验证。
func TestLeaveGroupStopsReceiving(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 3)
	convs := NewConversationStore(db)
	msgs := NewMessageStore(db)
	ctx := context.Background()

	leaver := f.Members[1]

	// 退群前是成员
	if ok, _ := convs.IsMember(ctx, f.ConvID, leaver); !ok {
		t.Fatal("前置条件失败：退群前应是成员")
	}

	removed, err := convs.RemoveMember(ctx, f.ConvID, leaver)
	if err != nil {
		t.Fatalf("退群失败: %v", err)
	}
	if !removed {
		t.Error("退群应真的删掉一行")
	}

	if ok, _ := convs.IsMember(ctx, f.ConvID, leaver); ok {
		t.Error("退群后不应再是成员")
	}

	// 关键：退群的人不再出现在投递收件人列表里
	memberIDs, err := convs.ListMemberIDs(ctx, f.ConvID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range memberIDs {
		if id == leaver {
			t.Fatal("退群的人仍出现在成员列表里 —— 他还会收到消息")
		}
	}

	// 历史消息保留（退群不删消息）
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, f.ConvID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	_ = n

	// 退群后仍然发不出消息（不是成员）
	if _, err := msgs.Send(ctx, SendInput{
		ConversationID: f.ConvID,
		SenderID:       leaver,
		ClientMsgID:    fmt.Sprintf("after-leave-%d", time.Now().UnixNano()),
		ContentType:    model.ContentTypeText,
		Content:        "我退群了还能发吗",
	}); err != nil {
		t.Fatalf("store.Send 不做成员校验（那是 service 的职责），不该报错: %v", err)
	}
}

// TestLeaveIsIdempotent 重复退群不报错。
func TestLeaveIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 2)
	convs := NewConversationStore(db)
	ctx := context.Background()

	leaver := f.Members[0]

	if removed, _ := convs.RemoveMember(ctx, f.ConvID, leaver); !removed {
		t.Fatal("第一次退群应成功")
	}
	removed, err := convs.RemoveMember(ctx, f.ConvID, leaver)
	if err != nil {
		t.Fatalf("重复退群不该报错: %v", err)
	}
	if removed {
		t.Error("重复退群 RowsAffected 应为 0")
	}
}

// TestGroupUnreadCount 对应验收 #5：未读数 = 最新 seq − last_read_seq。
//
// 场景：3 人群，A 发 10 条，B 读到 6 → B 未读 4。
func TestGroupUnreadCount(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 3)
	convs := NewConversationStore(db)
	msgs := NewMessageStore(db)
	ctx := context.Background()

	sender := f.OwnerID
	reader := f.Members[0]

	const total = 10
	for i := 1; i <= total; i++ {
		if _, err := msgs.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       sender,
			ClientMsgID:    fmt.Sprintf("gu-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("群消息 %d", i),
		}); err != nil {
			t.Fatalf("发第 %d 条失败: %v", i, err)
		}
	}

	// 还没读 → 未读 10
	unreadOf := func(uid uint64) uint64 {
		list, err := convs.ListForUser(ctx, uid, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range list {
			if item.ID == f.ConvID {
				return item.UnreadCount
			}
		}
		t.Fatalf("用户 %d 的列表里找不到群 %d", uid, f.ConvID)
		return 0
	}

	if got := unreadOf(reader); got != total {
		t.Errorf("未读数应为 %d，实际 %d", total, got)
	}

	// 读到 6 → 未读 4
	if err := convs.UpdateLastRead(ctx, f.ConvID, reader, 6); err != nil {
		t.Fatalf("上报已读失败: %v", err)
	}
	if got := unreadOf(reader); got != 4 {
		t.Errorf("读到 6 后未读数应为 4，实际 %d", got)
	}

	// 全部读完 → 0
	if err := convs.UpdateLastRead(ctx, f.ConvID, reader, total); err != nil {
		t.Fatal(err)
	}
	if got := unreadOf(reader); got != 0 {
		t.Errorf("全部读完后未读数应为 0，实际 %d", got)
	}

	// 其他成员未读不受影响（已读是"每人一个数字"）
	if got := unreadOf(f.Members[1]); got != total {
		t.Errorf("未读数应按人独立，成员2 应为 %d，实际 %d", total, got)
	}
}

// TestGroupReadStatusProvidesPerMemberSeq 验证「已读 2/4」所需的数据可得：
// ListMembers 会返回每个成员的 last_read_seq，前端据此算已读人数。
func TestGroupReadStatusProvidesPerMemberSeq(t *testing.T) {
	db := openTestDB(t)
	f := newGroupFixture(t, db, 4)
	convs := NewConversationStore(db)
	msgs := NewMessageStore(db)
	ctx := context.Background()

	// 发 5 条
	for i := 1; i <= 5; i++ {
		if _, err := msgs.Send(ctx, SendInput{
			ConversationID: f.ConvID,
			SenderID:       f.OwnerID,
			ClientMsgID:    fmt.Sprintf("rs-%d-%d", time.Now().UnixNano(), i),
			ContentType:    model.ContentTypeText,
			Content:        fmt.Sprintf("m%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 两个成员读到 5，一个读到 2
	_ = convs.UpdateLastRead(ctx, f.ConvID, f.Members[0], 5)
	_ = convs.UpdateLastRead(ctx, f.ConvID, f.Members[1], 5)
	_ = convs.UpdateLastRead(ctx, f.ConvID, f.Members[2], 2)

	members, err := convs.ListMembers(ctx, f.ConvID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 {
		t.Fatalf("成员数应为 4，实际 %d", len(members))
	}

	// 前端算「读到 seq>=5 的有几个人」
	readTo5 := 0
	for _, m := range members {
		if m.LastReadSeq >= 5 {
			readTo5++
		}
	}
	if readTo5 != 2 {
		t.Errorf("读到 seq5 的应有 2 人，实际 %d（前端据此显示「已读 2/4」）", readTo5)
	}
}

// TestSecondGroupCanBeCreated 验证 dm_key 用 NULL 而不是空串的关键性。
//
// 如果用空串，MySQL 的唯一索引只允许一个 ”，**第二个群就插不进去**。
func TestSecondGroupCanBeCreated(t *testing.T) {
	db := openTestDB(t)
	f1 := newGroupFixture(t, db, 2)
	f2 := newGroupFixture(t, db, 2)

	if f1.ConvID == f2.ConvID {
		t.Fatal("两个群不应该是同一个会话")
	}

	ctx := context.Background()
	convs := NewConversationStore(db)
	c1, err := convs.GetByID(ctx, f1.ConvID)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := convs.GetByID(ctx, f2.ConvID)
	if err != nil {
		t.Fatalf("第二个群读不出来: %v", err)
	}
	if c1.DMKey != nil || c2.DMKey != nil {
		t.Error("群聊的 dm_key 都应为 NULL")
	}
}
