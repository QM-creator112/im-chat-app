package ws

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// presenceTimeout 查询好友列表的超时。
const presenceTimeout = 3 * time.Second

// FriendIDsFunc 返回「需要接收该用户在线状态变化」的用户 ID 列表。
//
// 为什么用函数注入而不是直接依赖 store：
// 架构文档（docs/02-设计.md §一）规定「只有 service 层可以访问 store 层，
// handler 和 ws 不许直接碰数据库」。注入一个函数既守住了边界，
// 又让 Hub 可以脱离数据库做单元测试（测试里传一个假函数即可）。
//
// 传 nil 表示不广播在线状态（例如 Sprint 2 阶段还没有好友数据，
// 或单元测试不关心 presence）。
type FriendIDsFunc func(ctx context.Context, userID uint64) []uint64

// Hub 维护「用户 → 他的所有连接」的映射。
//
// 为什么是 map[uint64]map[*Client]struct{}：
//   - 外层 key 是 userID：投递消息时按 userID 找连接
//   - 内层是集合：一个用户可能在多个浏览器/设备同时登录（多端在线）
//   - 内层用 map[*Client]struct{} 而不是 slice：
//     struct{} 不占内存，删除是 O(1)，不用遍历
type Hub struct {
	mu    sync.RWMutex
	conns map[uint64]map[*Client]struct{}
	// friends 见 FriendIDsFunc 的说明
	friends FriendIDsFunc
}

// NewHub 构造 Hub。friends 可以为 nil。
func NewHub(friends FriendIDsFunc) *Hub {
	return &Hub{
		conns:   make(map[uint64]map[*Client]struct{}),
		friends: friends,
	}
}

// register 注册一条连接。
func (h *Hub) register(c *Client) {
	h.mu.Lock()

	set, ok := h.conns[c.userID]
	if !ok {
		set = make(map[*Client]struct{})
		h.conns[c.userID] = set
	}
	firstConn := len(set) == 0
	set[c] = struct{}{}
	n := len(set)

	h.mu.Unlock()

	slog.Info("ws 连接建立", "user_id", c.userID, "该用户连接数", n)

	// 只在「从离线变在线」时广播，不是每次连接都广播 ——
	// 否则用户开第三个浏览器会向好友重复推三次上线
	if firstConn {
		go h.broadcastPresence(c.userID, true)
	}
}

// unregister 注销一条连接。
func (h *Hub) unregister(c *Client) {
	if !c.markUnregistered() {
		return // 已经注销过，避免重复扣减与重复广播
	}

	h.mu.Lock()
	set, ok := h.conns[c.userID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(set, c)
	remaining := len(set)
	lastConn := remaining == 0
	if lastConn {
		delete(h.conns, c.userID)
	}
	h.mu.Unlock()

	slog.Info("ws 连接关闭", "user_id", c.userID, "剩余连接数", remaining)

	if lastConn {
		go h.broadcastPresence(c.userID, false)
	}
}

// SendToUser 把消息投给某个用户的所有在线连接，返回成功投递的连接数。
//
// 返回 0 表示该用户当前不在线 —— 这**不是错误**，
// 调用方应当跳过在线投递，等对方上线后按游标补拉。
func (h *Hub) SendToUser(userID uint64, payload []byte) int {
	// ⚠️ 关键：先在读锁内把连接复制出来，**释放锁之后才发送**。
	//
	// 如果持着读锁去发送，而发送可能阻塞（慢客户端），
	// 所有 register/unregister 都会被堵住 —— 那是一个真实的性能陷阱。
	h.mu.RLock()
	set := h.conns[userID]
	clients := make([]*Client, 0, len(set))
	for c := range set {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	sent := 0
	for _, c := range clients {
		if c.trySend(payload) {
			sent++
		}
	}
	return sent
}

// Connections 返回当前连接总数（不是用户数）。
//
// 给 /healthz 用 —— 压测时可以直接从这里看承载量，不用另开工具。
func (h *Hub) Connections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	n := 0
	for _, set := range h.conns {
		n += len(set)
	}
	return n
}

// OnlineUsers 返回当前在线**用户数**（一个用户多端只算一个）。
func (h *Hub) OnlineUsers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// IsOnline 判断某个用户是否在线。
func (h *Hub) IsOnline(userID uint64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[userID]
	return ok
}

// ConnectionCount 返回某个用户的连接数（多端）。
func (h *Hub) ConnectionCount(userID uint64) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[userID])
}

// CloseAll 关闭所有连接（优雅退出时调用）。
//
// 顺序：先从注册表里摘掉，再逐个关闭。
// 这样即使某个连接的关闭过程很慢，注册表已经是干净的状态。
func (h *Hub) CloseAll() {
	h.mu.Lock()
	clients := make([]*Client, 0, len(h.conns))
	for _, set := range h.conns {
		for c := range set {
			clients = append(clients, c)
		}
	}
	h.conns = make(map[uint64]map[*Client]struct{})
	h.mu.Unlock()

	slog.Info("ws 关闭全部连接", "数量", len(clients))
	for _, c := range clients {
		// 用优雅关闭（只发信号），让 writePump 有机会发出 1000 关闭帧。
		// 直接掐连接的话，客户端会看到 1006 异常断开，
		// 误以为是自己的网络问题 —— 而这个"误以为"会让排查方向完全跑偏。
		c.Close()
	}
}

// broadcastPresence 把某个用户的在线状态变化推给他的好友。
//
// 独立成方法并在 goroutine 里调用，是因为它要查数据库（好友列表），
// 而 register/unregister 处在连接建立/关闭的关键路径上，不能被 DB 查询拖慢。
func (h *Hub) broadcastPresence(userID uint64, online bool) {
	if h.friends == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), presenceTimeout)
	defer cancel()

	// 防御：friends 是外部注入的实现，可能 panic（例如 nil map 解引用）。
	// 在线状态属于"锦上添花"，绝不该因为它让整个服务崩掉。
	var friendIDs []uint64
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("查询好友列表 panic", "user_id", userID, "panic", r)
			}
		}()
		friendIDs = h.friends(ctx, userID)
	}()

	if len(friendIDs) == 0 {
		return
	}

	payload := MustEncode(TypePresence, "", PresenceData{UserID: userID, Online: online})
	notified := 0
	for _, fid := range friendIDs {
		if fid == userID {
			continue // 不推给自己
		}
		if h.SendToUser(fid, payload) > 0 {
			notified++
		}
	}
	if notified > 0 {
		slog.Debug("广播在线状态", "user_id", userID, "online", online, "已通知好友数", notified)
	}
}
