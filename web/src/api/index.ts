import { http } from './client'
import type {
  AuthResult,
  Conversation,
  Member,
  MessagePage,
  Page,
  SyncResult,
  User,
} from './types'

/**
 * 所有 HTTP 接口，一一对应 docs/api.md §2.1 的清单。
 *
 * 集中在一个文件里的好处：改接口时只需要看这一个文件，
 * 而且能一眼看出「前端到底用了契约里的哪些接口」。
 */

// ---------- 账号 ----------

export function register(input: {
  username: string
  password: string
  nickname?: string
}): Promise<{ userId: number }> {
  return http.post('/auth/register', input)
}

export function login(input: { username: string; password: string }): Promise<AuthResult> {
  return http.post('/auth/login', input)
}

export function logout(refreshToken: string): Promise<unknown> {
  return http.post('/auth/logout', { refreshToken })
}

export function me(): Promise<User> {
  return http.get('/users/me')
}

export function searchUsers(keyword: string): Promise<Page<User>> {
  return http.get(`/users?keyword=${encodeURIComponent(keyword)}`)
}

// ---------- 好友 ----------

export function listFriends(): Promise<Page<User>> {
  return http.get('/friends')
}

export function addFriend(friendId: number): Promise<unknown> {
  return http.post('/friends', { friendId })
}

// ---------- 会话 ----------

export function listConversations(limit = 50): Promise<Page<Conversation>> {
  return http.get(`/conversations?limit=${limit}`)
}

/** 创建单聊（type=1）或群聊（type=2）。单聊是幂等的：已存在就返回同一个 id。 */
export function createConversation(input: {
  type: number
  memberIds: number[]
  title?: string
}): Promise<{ conversationId: number }> {
  return http.post('/conversations', input)
}

/** 确保与某人的单聊会话存在，返回 conversationId。 */
export async function ensureDirectConversation(peerId: number): Promise<number> {
  const res = await createConversation({ type: 1, memberIds: [peerId] })
  return res.conversationId
}

export function listMembers(convId: number): Promise<Page<Member>> {
  return http.get(`/conversations/${convId}/members`)
}

// ---------- 群管理（Sprint 5）----------

/**
 * 拉人入群。已存在的成员会被忽略（幂等），返回真正新增的人数。
 * 只有群成员能操作；非成员会被后端拒绝（3002）。
 */
export function addMembers(convId: number, userIds: number[]): Promise<{ added: number }> {
  return http.post(`/conversations/${convId}/members`, { userIds })
}

/**
 * 退群。群主不能退（后端返回 1001），要先转让群主或解散群。
 * 历史消息保留，只是以后收不到新消息。
 */
export function leaveGroup(convId: number): Promise<unknown> {
  return http.del(`/conversations/${convId}/members/me`)
}

// ---------- 消息 ----------

/**
 * 历史消息（向前翻页）。
 * @param beforeSeq 我本地最早那条的 seq；传 0 表示从最新开始
 */
export function listMessages(
  convId: number,
  beforeSeq = 0,
  limit = 30,
): Promise<MessagePage> {
  return http.get(`/conversations/${convId}/messages?beforeSeq=${beforeSeq}&limit=${limit}`)
}

/** 增量补拉（断线重连后调用） */
export function syncMessages(convId: number, sinceSeq: number): Promise<SyncResult> {
  return http.get(`/conversations/${convId}/sync?sinceSeq=${sinceSeq}`)
}

/** 上报已读位置（后端用 GREATEST，只前进不回退） */
export function markRead(convId: number, readSeq: number): Promise<unknown> {
  return http.post(`/conversations/${convId}/read`, { readSeq })
}
