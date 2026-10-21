import type { Message } from '@/api/types'

/**
 * 消息在界面上的三态。
 *
 * 对应原型第 ③ 屏（单聊·三态 + 已读）：
 *   sending 发送中 → sent 已发送 → （失败则 failed，可点击重试）
 */
export type MessageStatus = 'sending' | 'sent' | 'failed'

/**
 * 界面使用的消息模型。
 *
 * 与后端 Message 的差别：
 *   - `seq` 在服务端确认前是 0（乐观更新时还没分配）
 *   - 多了 `status`
 *   - 本地用自己的 `clientMsgId` 作为未确认消息的唯一键
 */
export interface ChatMessage {
  clientMsgId: string
  id: number
  conversationId: number
  seq: number
  senderId: number
  senderName?: string
  contentType: number
  content: string
  createdAt: number
  status: MessageStatus
}

/** 服务端消息 → 界面消息 */
export function fromServer(m: Message): ChatMessage {
  return {
    clientMsgId: m.clientMsgId,
    id: m.id,
    conversationId: m.conversationId,
    seq: m.seq,
    senderId: m.senderId,
    senderName: m.senderName,
    contentType: m.contentType,
    content: m.content,
    createdAt: m.createdAt,
    status: 'sent',
  }
}

/** 乐观更新：用户点发送的瞬间先插一条「发送中」 */
export function createPending(input: {
  conversationId: number
  senderId: number
  senderName?: string
  content: string
  clientMsgId: string
  contentType?: number
  createdAt?: number
}): ChatMessage {
  return {
    clientMsgId: input.clientMsgId,
    id: 0,
    conversationId: input.conversationId,
    seq: 0,
    senderId: input.senderId,
    senderName: input.senderName,
    contentType: input.contentType ?? 1,
    content: input.content,
    createdAt: input.createdAt ?? Date.now(),
    status: 'sending',
  }
}

/** 去重键：已确认的按 seq，未确认的按 clientMsgId。 */
function keyOf(m: ChatMessage): string {
  return m.seq > 0 ? `s:${m.seq}` : `c:${m.clientMsgId}`
}

/**
 * 排序：服务端消息按 seq 升序；未确认（seq=0）的排在最后。
 *
 * 为什么不能按 createdAt 排全部：客户端的本地时钟可能和服务端差几十秒，
 * 一旦混排会让消息顺序错乱（用户会看到自己刚发的消息插到历史中间）。
 * **seq 是唯一的顺序权威**，只有还没拿到 seq 的才用本地时间凑合。
 */
export function sortMessages(list: ChatMessage[]): ChatMessage[] {
  return list.slice().sort((a, b) => {
    const aPending = a.seq === 0
    const bPending = b.seq === 0
    if (aPending !== bPending) return aPending ? 1 : -1
    if (!aPending && a.seq !== b.seq) return a.seq - b.seq
    if (a.createdAt !== b.createdAt) return a.createdAt - b.createdAt
    // 最后用 clientMsgId 兜底，保证排序稳定（避免 UI 抖动）
    return a.clientMsgId < b.clientMsgId ? -1 : a.clientMsgId > b.clientMsgId ? 1 : 0
  })
}

/**
 * 合并消息并去重。
 *
 * 三种情况都要能正确处理：
 *   1. 同一 seq 重复到达（服务端「至少一次」投递 + 重连补拉会重叠）→ 只留一条
 *   2. 本地「发送中」的消息在补拉时从服务端回来了 → 用服务端版本替换本地版本
 *   3. 完全没见过的消息 → 追加
 */
export function upsertMessages(list: ChatMessage[], incoming: ChatMessage[]): ChatMessage[] {
  if (incoming.length === 0) return list

  const incomingClientIds = new Set(incoming.map((m) => m.clientMsgId))
  const map = new Map<string, ChatMessage>()

  for (const m of list) {
    // 本地未确认的，如果服务端已经有同 clientMsgId 的版本，就让位给服务端版本
    if (m.seq === 0 && incomingClientIds.has(m.clientMsgId)) continue
    map.set(keyOf(m), m)
  }
  for (const m of incoming) {
    map.set(keyOf(m), m)
  }
  return sortMessages(Array.from(map.values()))
}

/** 收到 send_ack：把「发送中」变成「已发送」，并落到正确的 seq 位置。 */
export function applyAck(
  list: ChatMessage[],
  clientMsgId: string,
  seq: number,
  createdAt: number,
): ChatMessage[] {
  const idx = list.findIndex((m) => m.clientMsgId === clientMsgId)
  if (idx < 0) return list
  const next = list.slice()
  const base = next[idx] as ChatMessage
  next[idx] = { ...base, seq, createdAt, status: 'sent' }
  return sortMessages(next)
}

/** 标记为发送失败（超时 / 断链 / 服务端报错）。 */
export function markFailed(list: ChatMessage[], clientMsgId: string): ChatMessage[] {
  const idx = list.findIndex((m) => m.clientMsgId === clientMsgId)
  if (idx < 0) return list
  const next = list.slice()
  next[idx] = { ...(next[idx] as ChatMessage), status: 'failed' }
  return next
}

/** 重试前把状态改回「发送中」。 */
export function markSending(list: ChatMessage[], clientMsgId: string): ChatMessage[] {
  const idx = list.findIndex((m) => m.clientMsgId === clientMsgId)
  if (idx < 0) return list
  const next = list.slice()
  next[idx] = { ...(next[idx] as ChatMessage), status: 'sending' }
  return next
}

/** 当前列表里最大的已确认 seq（用于 /sync 的 sinceSeq）。 */
export function maxSeq(list: ChatMessage[]): number {
  let max = 0
  for (const m of list) {
    if (m.seq > max) max = m.seq
  }
  return max
}

/** 移除某条消息（重试成功后被服务端版本取代时用）。 */
export function removeByClientId(list: ChatMessage[], clientMsgId: string): ChatMessage[] {
  return list.filter((m) => m.clientMsgId !== clientMsgId)
}
