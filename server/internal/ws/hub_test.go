package ws

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClient 构造一个「没有真实网络连接」的 Client。
//
// 注册表、背压、在线状态广播这些逻辑本来就不依赖真实 TCP，
// 所以单测里用假连接即可 —— 这样测试是纯内存的，毫秒级跑完。
func testClient(hub *Hub, userID uint64, bufSize int) *Client {
	return &Client{
		userID: userID,
		hub:    hub,
		send:   make(chan []byte, bufSize),
		done:   make(chan struct{}),
	}
}

// drainOne 从 client 的发送缓冲里取一条消息，超时返回 nil。
func drainOne(c *Client, timeout time.Duration) []byte {
	select {
	case b := <-c.send:
		return b
	case <-time.After(timeout):
		return nil
	}
}

// ---------- 注册表 ----------

func TestHubRegisterUnregister(t *testing.T) {
	h := NewHub(nil)
	c1 := testClient(h, 1, 8)
	c2 := testClient(h, 1, 8) // 同一用户的第二个设备
	c3 := testClient(h, 2, 8)

	h.register(c1)
	h.register(c2)
	h.register(c3)

	if got := h.Connections(); got != 3 {
		t.Errorf("连接总数应为 3，实际 %d", got)
	}
	if got := h.OnlineUsers(); got != 2 {
		t.Errorf("在线用户数应为 2（用户 1 有两个设备），实际 %d", got)
	}
	if got := h.ConnectionCount(1); got != 2 {
		t.Errorf("用户 1 的连接数应为 2，实际 %d", got)
	}
	if !h.IsOnline(1) || !h.IsOnline(2) {
		t.Error("用户 1 与 2 都应在线")
	}

	// 注销一个设备后，用户 1 仍在线
	h.unregister(c1)
	if !h.IsOnline(1) {
		t.Error("用户 1 还留一个设备，应仍在线")
	}
	if got := h.Connections(); got != 2 {
		t.Errorf("连接总数应为 2，实际 %d", got)
	}

	// 注销最后一个设备后，用户 1 离线
	h.unregister(c2)
	if h.IsOnline(1) {
		t.Error("用户 1 的所有设备都断了，应离线")
	}
	if got := h.OnlineUsers(); got != 1 {
		t.Errorf("在线用户数应为 1，实际 %d", got)
	}
}

// TestUnregisterIsIdempotent 验证重复注销不会出错。
//
// 这条很重要：readPump 和 writePump 都会在退出时注销同一个连接，
// 加上 trySend 里的主动注销，同一个 Client 可能被注销多次。
// 如果不幂等，连接计数会多扣、离线广播会推多次。
func TestUnregisterIsIdempotent(t *testing.T) {
	h := NewHub(nil)
	c := testClient(h, 1, 8)
	h.register(c)

	h.unregister(c)
	h.unregister(c) // 第二次
	h.unregister(c) // 第三次

	if got := h.OnlineUsers(); got != 0 {
		t.Errorf("重复注销后在线用户数应为 0，实际 %d", got)
	}
	if got := h.Connections(); got != 0 {
		t.Errorf("重复注销后连接数应为 0，实际 %d", got)
	}
}

// ---------- 投递 ----------

func TestSendToUserDeliversToAllDevices(t *testing.T) {
	h := NewHub(nil)
	c1 := testClient(h, 1, 8)
	c2 := testClient(h, 1, 8)
	h.register(c1)
	h.register(c2)

	payload := []byte(`{"type":"push"}`)
	n := h.SendToUser(1, payload)

	if n != 2 {
		t.Errorf("多端都应收到，期望 2，实际 %d", n)
	}
	for i, c := range []*Client{c1, c2} {
		got := drainOne(c, 200*time.Millisecond)
		if got == nil {
			t.Errorf("第 %d 个设备没收到消息", i+1)
			continue
		}
		if string(got) != string(payload) {
			t.Errorf("第 %d 个设备收到的内容不对: %s", i+1, got)
		}
	}
}

// TestSendToOfflineUserReturnsZero 验证给离线用户投递返回 0 且不 panic。
func TestSendToOfflineUserReturnsZero(t *testing.T) {
	h := NewHub(nil)
	if n := h.SendToUser(999, []byte("x")); n != 0 {
		t.Errorf("离线用户应返回 0，实际 %d", n)
	}
}

// TestSlowClientIsDropped 是「背压 / 慢连接隔离」的核心测试。
//
// 场景：客户端拿到消息却不消费（弱网卡住 / 连接假死）。
// 期望：缓冲满时**直接断开该连接**，并立刻从注册表摘掉 ——
// 而不是无限缓冲（会在内存耗尽时崩溃）或静默丢弃（会丢消息）。
func TestSlowClientIsDropped(t *testing.T) {
	h := NewHub(nil)
	// 缓冲区只有 1，塞 2 条必然触发背压
	c := testClient(h, 1, 1)
	h.register(c)

	if !h.IsOnline(1) {
		t.Fatal("前置条件失败：客户端应已注册")
	}

	// 第一条进缓冲
	if n := h.SendToUser(1, []byte("msg-1")); n != 1 {
		t.Fatalf("第一条应投递成功，实际 %d", n)
	}
	// 第二条触发背压 → 断开
	if n := h.SendToUser(1, []byte("msg-2")); n != 0 {
		t.Errorf("慢连接应被断开，投递数应为 0，实际 %d", n)
	}

	if h.IsOnline(1) {
		t.Error("慢连接被断开后，用户应已离线（注册表要立刻摘掉，不能留半死状态）")
	}

	// 连接确实被关闭了
	select {
	case <-c.done:
	default:
		t.Error("连接应处于已关闭状态")
	}
}

// ---------- 在线状态广播 ----------

func TestPresenceBroadcastToFriends(t *testing.T) {
	// 用户 1 的好友是 2 和 3
	h := NewHub(func(_ context.Context, userID uint64) []uint64 {
		if userID == 1 {
			return []uint64{2, 3}
		}
		return nil
	})

	friend2 := testClient(h, 2, 8)
	friend3 := testClient(h, 3, 8)
	h.register(friend2)
	h.register(friend3)

	// 用户 1 上线
	c1 := testClient(h, 1, 8)
	h.register(c1)

	// 广播在 goroutine 里做，稍等
	for i, f := range []*Client{friend2, friend3} {
		got := drainOne(f, 500*time.Millisecond)
		if got == nil {
			t.Errorf("好友 %d 没收到上线通知", i+2)
			continue
		}
		if !strings.Contains(string(got), `"type":"presence"`) {
			t.Errorf("好友 %d 收到的不是 presence 消息: %s", i+2, got)
		}
		if !strings.Contains(string(got), `"online":true`) {
			t.Errorf("好友 %d 应收到 online:true: %s", i+2, got)
		}
		if !strings.Contains(string(got), `"userId":1`) {
			t.Errorf("好友 %d 应收到 userId:1: %s", i+2, got)
		}
	}
}

// TestPresenceOnlyOnFirstConnection 验证多端登录不会重复广播上线。
//
// 否则用户开第三个浏览器时，好友会收到三次「上线」通知。
func TestPresenceOnlyOnFirstConnection(t *testing.T) {
	var calls int32
	var mu sync.Mutex
	h := NewHub(func(_ context.Context, userID uint64) []uint64 {
		mu.Lock()
		calls++
		mu.Unlock()
		return []uint64{2}
	})

	friend := testClient(h, 2, 16)
	h.register(friend)

	// 用户 1 的第一个连接 → 应广播
	c1 := testClient(h, 1, 8)
	h.register(c1)
	if drainOne(friend, 500*time.Millisecond) == nil {
		t.Fatal("第一个连接上线时，好友应收到通知")
	}

	// 用户 1 的第二个连接（多端）→ 不应再广播
	c2 := testClient(h, 1, 8)
	h.register(c2)
	if extra := drainOne(friend, 300*time.Millisecond); extra != nil {
		t.Errorf("多端登录不应重复广播上线，但收到了: %s", extra)
	}

	// 关掉一个连接，用户仍有另一个在线 → 不应广播离线
	h.unregister(c1)
	if extra := drainOne(friend, 300*time.Millisecond); extra != nil {
		t.Errorf("仍有连接在线时不应广播离线，但收到了: %s", extra)
	}

	// 关掉最后一个连接 → 应广播离线
	h.unregister(c2)
	offline := drainOne(friend, 500*time.Millisecond)
	if offline == nil {
		t.Fatal("最后一个连接断开时，好友应收到离线通知")
	}
	if !strings.Contains(string(offline), `"online":false`) {
		t.Errorf("应是 offline 通知: %s", offline)
	}
}

// TestPresenceWithNilFriendsIsSafe 验证没配好友查询时不 panic。
func TestPresenceWithNilFriendsIsSafe(t *testing.T) {
	h := NewHub(nil) // friends 为 nil
	c := testClient(h, 1, 8)
	h.register(c)
	h.unregister(c)
	// 不 panic 即通过
	time.Sleep(50 * time.Millisecond)
}

// TestPresenceHandlesPanickingFriends 验证好友查询 panic 不会拖垮服务。
//
// 在线状态是"锦上添花"的能力，绝不该因为它让整个进程崩掉。
func TestPresenceHandlesPanickingFriends(t *testing.T) {
	h := NewHub(func(_ context.Context, _ uint64) []uint64 {
		panic("模拟坏掉的实现")
	})
	friend := testClient(h, 2, 8)
	h.register(friend)

	c := testClient(h, 1, 8)
	h.register(c) // 不应 panic

	// 等广播的 goroutine 跑完（它内部 recover 掉了）
	time.Sleep(100 * time.Millisecond)

	// 服务仍可用
	if !h.IsOnline(1) {
		t.Error("好友查询 panic 不应影响注册表")
	}
}

// ---------- 优雅退出 ----------

func TestCloseAll(t *testing.T) {
	h := NewHub(nil)
	c1 := testClient(h, 1, 8)
	c2 := testClient(h, 2, 8)
	h.register(c1)
	h.register(c2)

	h.CloseAll()

	if got := h.Connections(); got != 0 {
		t.Errorf("CloseAll 后应没有连接，实际 %d", got)
	}
	for i, c := range []*Client{c1, c2} {
		select {
		case <-c.done:
		default:
			t.Errorf("第 %d 个连接应已关闭", i+1)
		}
	}
}

// ---------- 并发安全 ----------

// TestHubConcurrentAccess 用竞态检测器验证注册表的并发安全。
//
// 本地 Windows 跑 -race 需要 cgo（要 gcc），CI 的 ubuntu runner 会跑；
// 这里即使没开 -race 也能验证逻辑不会死锁或 panic。
func TestHubConcurrentAccess(t *testing.T) {
	h := NewHub(nil)
	const goroutines = 20
	const perGoroutine = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			userID := uint64(g%5 + 1) // 5 个用户各 4 条连接
			for i := 0; i < perGoroutine; i++ {
				c := testClient(h, userID, 64)
				h.register(c)
				h.SendToUser(userID, []byte("broadcast"))
				h.OnlineUsers()
				h.Connections()
				h.unregister(c)
			}
		}(g)
	}
	wg.Wait()

	if got := h.Connections(); got != 0 {
		t.Errorf("全部注销后连接数应为 0，实际 %d", got)
	}
}
