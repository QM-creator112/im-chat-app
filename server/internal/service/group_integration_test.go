package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QM-creator112/im-chat-app/server/internal/config"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// Sprint 5：群管理的**业务规则**测试。
//
// 这些规则属于 service 层（不是 store）：store 只做数据，
// 而「群主不能退群」「非成员不能拉人」是业务判断。
// 需要真实 MySQL（因为要验证唯一键、NULL 语义等数据库行为）。

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("跳过：读取配置失败（%v）", err)
	}
	db, err := store.Open(cfg.DB.DSN())
	if err != nil {
		t.Skipf("跳过：连不上 MySQL（%v）", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type groupSvcFixture struct {
	svc      *ConversationService
	OwnerID  uint64
	Member   uint64
	Outsider uint64
	ConvID   uint64
	users    *store.UserStore
	convs    *store.ConversationStore
	db       *sql.DB
}

func newGroupSvcFixture(t *testing.T, db *sql.DB) *groupSvcFixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	users := store.NewUserStore(db)
	convs := store.NewConversationStore(db)
	svc := NewConversationService(convs, users)

	mk := func(tag string) uint64 {
		id, err := users.CreateUser(ctx,
			fmt.Sprintf("gs_%d_%s", suffix, tag), strings.Repeat("x", 60), "群业务"+tag)
		if err != nil {
			t.Fatalf("建用户失败: %v", err)
		}
		return id
	}

	f := &groupSvcFixture{
		svc:      svc,
		users:    users,
		convs:    convs,
		db:       db,
		OwnerID:  mk("owner"),
		Member:   mk("member"),
		Outsider: mk("outsider"),
	}

	convID, err := svc.CreateGroup(ctx, f.OwnerID, "业务规则测试群", []uint64{f.Member})
	if err != nil {
		t.Fatalf("建群失败: %v", err)
	}
	f.ConvID = convID

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`, convID)
		_, _ = db.ExecContext(ctx, `DELETE FROM conversation_members WHERE conversation_id = ?`, convID)
		_, _ = db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, convID)
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id IN (?, ?, ?)`,
			f.OwnerID, f.Member, f.Outsider)
	})
	return f
}

// TestOwnerCannotLeaveGroup 群主退群必须被拒绝。
//
// 理由：群主一退，群就变成无主状态，没人能再拉人/管理。
// 正确做法是先转让群主或解散群（转让功能本项目暂不做，但要给出明确提示）。
func TestOwnerCannotLeaveGroup(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	err := f.svc.Leave(ctx, f.ConvID, f.OwnerID)
	if err == nil {
		t.Fatal("群主退群应当被拒绝")
	}
	if !errors.Is(err, ErrOwnerCannotLeave) {
		t.Errorf("应返回 ErrOwnerCannotLeave，实际: %v", err)
	}

	// 群主仍是成员
	ok, _ := f.convs.IsMember(ctx, f.ConvID, f.OwnerID)
	if !ok {
		t.Error("被拒绝后群主应仍是成员")
	}
}

// TestMemberCanLeaveGroup 普通成员可以退群。
func TestMemberCanLeaveGroup(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	if err := f.svc.Leave(ctx, f.ConvID, f.Member); err != nil {
		t.Fatalf("普通成员退群应当成功: %v", err)
	}
	ok, _ := f.convs.IsMember(ctx, f.ConvID, f.Member)
	if ok {
		t.Error("退群后不应再是成员")
	}
}

// TestNonMemberCannotAddMembers 非成员不能往群里拉人。
//
// 这条是**安全边界**：如果只校验"是群聊"而不校验"你是不是成员"，
// 任何人猜到 conversation_id 就能往别人的群里塞人。
func TestNonMemberCannotAddMembers(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	_, err := f.svc.AddMembers(ctx, f.ConvID, f.Outsider, []uint64{f.Outsider})
	if err == nil {
		t.Fatal("非成员拉人应当被拒绝")
	}
	if !errors.Is(err, ErrNotMember) {
		t.Errorf("应返回 ErrNotMember，实际: %v", err)
	}

	// 确认真的没被加进去
	ok, _ := f.convs.IsMember(ctx, f.ConvID, f.Outsider)
	if ok {
		t.Error("被拒绝后不应成为成员")
	}
}

// TestMemberCanAddOthers 群成员可以拉人。
func TestMemberCanAddOthers(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	added, err := f.svc.AddMembers(ctx, f.ConvID, f.Member, []uint64{f.Outsider})
	if err != nil {
		t.Fatalf("群成员拉人应当成功: %v", err)
	}
	if added != 1 {
		t.Errorf("应新增 1 人，实际 %d", added)
	}
	ok, _ := f.convs.IsMember(ctx, f.ConvID, f.Outsider)
	if !ok {
		t.Error("拉人后应成为成员")
	}
}

// TestAddMembersToDirectChatRejected 单聊不能拉人。
func TestAddMembersToDirectChatRejected(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	dmID, err := f.svc.EnsureDM(ctx, f.OwnerID, f.Member)
	if err != nil {
		t.Fatalf("建单聊失败: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, dmID) })

	_, err = f.svc.AddMembers(ctx, dmID, f.OwnerID, []uint64{f.Outsider})
	if err == nil {
		t.Fatal("单聊不能拉人")
	}
	if !errors.Is(err, ErrNotGroup) {
		t.Errorf("应返回 ErrNotGroup，实际: %v", err)
	}
}

// TestLeaveDirectChatRejected 单聊不能"退群"（否则就变成删好友了，语义不清）。
func TestLeaveDirectChatRejected(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	dmID, err := f.svc.EnsureDM(ctx, f.OwnerID, f.Member)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, dmID) })

	if err := f.svc.Leave(ctx, dmID, f.Member); !errors.Is(err, ErrNotGroup) {
		t.Errorf("单聊退群应返回 ErrNotGroup，实际: %v", err)
	}
}

// TestAddMembersRejectsInvalidUser 有无效用户 ID 时整体失败（不半成功）。
func TestAddMembersRejectsInvalidUser(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	const ghostID = uint64(999_999_999)
	added, err := f.svc.AddMembers(ctx, f.ConvID, f.OwnerID, []uint64{f.Outsider, ghostID})
	if err == nil {
		t.Fatal("含无效用户时应当整体失败")
	}
	if added != 0 {
		t.Errorf("失败时新增数应为 0，实际 %d", added)
	}

	// 关键：合法的那个也不该被加进去（避免"半成功"）
	ok, _ := f.convs.IsMember(ctx, f.ConvID, f.Outsider)
	if ok {
		t.Error("整体失败时不应有任何一个成员被加入")
	}
}

// TestCreateGroupValidatesMembers 建群时成员无效要拒绝。
func TestCreateGroupValidatesMembers(t *testing.T) {
	db := openTestDB(t)
	f := newGroupSvcFixture(t, db)
	ctx := context.Background()

	if _, err := f.svc.CreateGroup(ctx, f.OwnerID, "坏群", []uint64{999_999_999}); err == nil {
		t.Fatal("含无效成员的建群应当失败")
	}

	// 空成员群要拒绝（一个人也能建群没有意义，且容易产生脏数据）
	if _, err := f.svc.CreateGroup(ctx, f.OwnerID, "空群", nil); err == nil {
		t.Fatal("空成员的建群应当失败")
	}
}
