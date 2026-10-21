import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { ElMessage } from 'element-plus'

import * as api from '@/api'
import type {
  Conversation,
  WsEnvelope,
  WsErrorData,
  WsPresenceData,
  WsPushData,
  WsReadPushData,
  WsSendAckData,
  WsSendData,
} from '@/api/types'
import { getAccessToken } from '@/api/token'
import { WsClient, type WsStatus } from '@/ws/client'
import {
  applyAck,
  createPending,
  fromServer,
  markFailed,
  markSending,
  maxSeq,
  upsertMessages,
  type ChatMessage,
} from '@/utils/chatMessage'
import { uuidv7 } from '@/utils/uuid'
import { useAuthStore } from './auth'

/** 发送后多久没收到 ack 就判失败（毫秒）。 */
const ACK_TIMEOUT_MS = 5000
/** 历史消息每页条数 */
const HISTORY_PAGE_SIZE = 30

export const useChatStore = defineStore('chat', () => {
  const auth = useAuthStore()

  // ---------------- 状态 ----------------
  const conversations = ref<Conversation[]>([])
  const activeConvId = ref<number | null>(null)
  /** conversationId → 消息列表 */
  const messages = ref<Record<number, ChatMessage[]>>({})
  const loadingConversations = ref(false)
  const loadingMessages = ref(false)
  const hasMore = ref<Record<number, boolean>>({})
  const sendingCount = ref(0)

  // 连接状态
  const wsStatus = ref<WsStatus>('idle')
  const wsAttempt = ref(0)
  const wsNextDelayMs = ref<number | undefined>(undefined)
  /** 在线用户集合（来自 presence 推送） */
  const onlineUsers = ref<Set<number>>(new Set())
  /** 对方已读位置：convId → 对方读到的 seq */
  const peerReadSeq = ref<Record<number, number>>({})

  // ---------------- 派生 ----------------
  const activeConversation = computed<Conversation | null>(
    () => conversations.value.find((c) => c.id === activeConvId.value) ?? null,
  )
  const activeMessages = computed<ChatMessage[]>(() =>
    activeConvId.value === null ? [] : (messages.value[activeConvId.value] ?? []),
  )
  const isConnected = computed(() => wsStatus.value === 'open')
  const totalUnread = computed(() => conversations.value.reduce((s, c) => s + c.unreadCount, 0))

  // ---------------- 内部 ----------------
  let ws: WsClient | null = null
  /** reqId → clientMsgId，用于把服务端 error 映射回具体那条消息 */
  const reqToClient = new Map<string, string>()
  /** clientMsgId → ack 超时定时器 */
  const ackTimers = new Map<string, ReturnType<typeof setTimeout>>()

  function listOf(convId: number): ChatMessage[] {
    return messages.value[convId] ?? []
  }

  function setList(convId: number, list: ChatMessage[]): void {
    messages.value = { ...messages.value, [convId]: list }
  }

  function clearAckTimer(clientMsgId: string): void {
    const t = ackTimers.get(clientMsgId)
    if (t !== undefined) {
      clearTimeout(t)
      ackTimers.delete(clientMsgId)
    }
  }

  /** 更新会话摘要（收到推送 / 自己发送 / 已读后调用） */
  function touchConversation(
    convId: number,
    patch: Partial<Conversation> & { bumpTime?: boolean },
  ): void {
    const { bumpTime, ...rest } = patch
    conversations.value = conversations.value
      .map((c) => (c.id === convId ? { ...c, ...rest, ...(bumpTime ? { lastMsgAt: Date.now() } : {}) } : c))
      .sort((a, b) => b.lastMsgAt - a.lastMsgAt)
  }

  // ---------------- 会话 ----------------
  async function loadConversations(): Promise<void> {
    loadingConversations.value = true
    try {
      const page = await api.listConversations()
      conversations.value = [...page.items].sort((a, b) => b.lastMsgAt - a.lastMsgAt)
      // 首次进入时自动选中第一个会话
      if (activeConvId.value === null && conversations.value.length > 0) {
        await selectConversation((conversations.value[0] as Conversation).id)
      }
    } catch (e) {
      ElMessage.error(`加载会话失败：${(e as Error).message}`)
    } finally {
      loadingConversations.value = false
    }
  }

  async function selectConversation(convId: number): Promise<void> {
    activeConvId.value = convId
    if (messages.value[convId] === undefined) {
      await loadInitialMessages(convId)
    }
    await markReadIfNeeded(convId)
  }

  async function loadInitialMessages(convId: number): Promise<void> {
    loadingMessages.value = true
    try {
      const page = await api.listMessages(convId, 0, HISTORY_PAGE_SIZE)
      const list = upsertMessages([], page.items.map(fromServer))
      setList(convId, list)
      hasMore.value = { ...hasMore.value, [convId]: page.hasMore }
    } catch (e) {
      ElMessage.error(`加载消息失败：${(e as Error).message}`)
    } finally {
      loadingMessages.value = false
    }
  }

  /** 向上翻页（加载更早的消息） */
  async function loadMoreHistory(): Promise<void> {
    const convId = activeConvId.value
    if (convId === null) return
    if (hasMore.value[convId] !== true) return

    const list = listOf(convId)
    const earliest = list.find((m) => m.seq > 0)
    if (!earliest) return

    loadingMessages.value = true
    try {
      const page = await api.listMessages(convId, earliest.seq, HISTORY_PAGE_SIZE)
      setList(convId, upsertMessages(list, page.items.map(fromServer)))
      hasMore.value = { ...hasMore.value, [convId]: page.hasMore }
    } catch (e) {
      ElMessage.error(`加载历史失败：${(e as Error).message}`)
    } finally {
      loadingMessages.value = false
    }
  }

  // ---------------- 已读 ----------------
  async function markReadIfNeeded(convId?: number): Promise<void> {
    const id = convId ?? activeConvId.value
    if (id === null) return
    const conv = conversations.value.find((c) => c.id === id)
    if (!conv) return
    const localMax = maxSeq(listOf(id))
    const target = Math.max(localMax, conv.latestSeq)
    if (target <= conv.myLastReadSeq) return

    try {
      await api.markRead(id, target)
      touchConversation(id, { myLastReadSeq: target, unreadCount: 0 })
    } catch {
      // 已读上报失败不打扰用户：下次收到消息或切会话时会再试
    }
  }

  // ---------------- 发送 ----------------
  /**
   * 发送消息（乐观更新 + 三态）。
   *
   * ⚠️ 关键点：clientMsgId 在**这里**生成一次并保存在消息对象里，
   * 重试时（retryMessage）**复用同一个**。每次重试都生成新的，
   * 服务端的幂等就完全失效了 —— 这是最容易写错的地方。
   */
  function sendMessage(content: string): void {
    const convId = activeConvId.value
    const userId = auth.user?.id
    if (convId === null || userId === undefined) return

    const text = content.trim()
    if (text === '') return

    const clientMsgId = uuidv7()
    const pending = createPending({
      conversationId: convId,
      senderId: userId,
      senderName: auth.displayName,
      content: text,
      clientMsgId,
    })

    setList(convId, upsertMessages(listOf(convId), [pending]))
    touchConversation(convId, { lastMsg: text, bumpTime: true })
    transmit(pending)
  }

  /** 把一条 pending 消息真正发出去（sendMessage 与 retryMessage 共用） */
  function transmit(msg: ChatMessage): void {
    const reqId = `r-${msg.clientMsgId.slice(-12)}`
    reqToClient.set(reqId, msg.clientMsgId)

    const data: WsSendData = {
      clientMsgId: msg.clientMsgId,
      conversationId: msg.conversationId,
      contentType: msg.contentType,
      content: msg.content,
    }

    sendingCount.value += 1
    const ok = ws?.send({ type: 'send', reqId, data }) ?? false
    if (!ok) {
      sendingCount.value = Math.max(0, sendingCount.value - 1)
      setList(msg.conversationId, markFailed(listOf(msg.conversationId), msg.clientMsgId))
      ElMessage.warning('连接已断开，消息待重试')
      return
    }

    // ack 超时保护：服务端没回就把消息标失败，让用户能点重试
    clearAckTimer(msg.clientMsgId)
    const timer = setTimeout(() => {
      ackTimers.delete(msg.clientMsgId)
      sendingCount.value = Math.max(0, sendingCount.value - 1)
      const list = listOf(msg.conversationId)
      const still = list.find((m) => m.clientMsgId === msg.clientMsgId)
      if (still && still.status === 'sending') {
        setList(msg.conversationId, markFailed(list, msg.clientMsgId))
      }
    }, ACK_TIMEOUT_MS)
    ackTimers.set(msg.clientMsgId, timer)
  }

  /** 重试一条失败消息：复用原 clientMsgId */
  function retryMessage(clientMsgId: string): void {
    const convId = activeConvId.value
    if (convId === null) return
    const list = listOf(convId)
    const msg = list.find((m) => m.clientMsgId === clientMsgId)
    if (!msg || msg.status !== 'failed') return

    setList(convId, markSending(list, clientMsgId))
    transmit({ ...msg, status: 'sending' })
  }

  // ---------------- WebSocket ----------------
  function startSocket(): void {
    if (ws) return
    ws = new WsClient({
      getToken: () => getAccessToken(),
      onMessage: handleWsMessage,
      onStatus: (status, info) => {
        wsStatus.value = status
        wsAttempt.value = info.attempt
        wsNextDelayMs.value = info.nextDelayMs
      },
      onReconnected: () => {
        // 重连成功：逐会话补拉断线期间遗漏的消息
        void syncAllConversations()
        ElMessage.success('已重新连接')
      },
    })
    ws.connect()
  }

  function stopSocket(): void {
    ws?.close()
    ws = null
    wsStatus.value = 'closed'
  }

  /** 断线重连后的补拉：对每个会话按本地 maxSeq 调 /sync */
  async function syncAllConversations(): Promise<void> {
    for (const conv of [...conversations.value]) {
      const localMax = maxSeq(listOf(conv.id))
      try {
        const res = await api.syncMessages(conv.id, localMax)
        if (res.hasGap) {
          // 增量已不可用（离线太久 / 超过保留窗口）→ 降级为重新拉最近 N 条
          await loadInitialMessages(conv.id)
        } else if (res.items.length > 0) {
          setList(conv.id, upsertMessages(listOf(conv.id), res.items.map(fromServer)))
        }
        touchConversation(conv.id, { latestSeq: res.latestSeq })
      } catch {
        // 单个会话补拉失败不影响其它会话
      }
    }
    await loadConversations()
    if (activeConvId.value !== null) await markReadIfNeeded(activeConvId.value)
  }

  /** WebSocket 消息分发 */
  function handleWsMessage(env: WsEnvelope): void {
    switch (env.type) {
      case 'pong':
        return

      case 'send_ack': {
        const d = env.data as WsSendAckData
        sendingCount.value = Math.max(0, sendingCount.value - 1)
        clearAckTimer(d.clientMsgId)
        const convId = findConvByClientMsgId(d.clientMsgId)
        if (convId === null) return
        setList(convId, applyAck(listOf(convId), d.clientMsgId, d.seq, d.createdAt))
        touchConversation(convId, { latestSeq: d.seq })
        // 自己发完也算已读
        void markReadIfNeeded(convId)
        return
      }

      case 'push': {
        const d = env.data as WsPushData
        // 推送里没有 clientMsgId（那是发送者本地的东西），
        // 用 convId+seq 合成一个稳定的去重键，保证重复推送只入一条。
        const incoming: ChatMessage = {
          clientMsgId: `srv-${d.conversationId}-${d.seq}`,
          id: 0,
          conversationId: d.conversationId,
          seq: d.seq,
          senderId: d.senderId,
          senderName: d.senderName,
          contentType: d.contentType,
          content: d.content,
          createdAt: d.createdAt,
          status: 'sent',
        }

        setList(d.conversationId, upsertMessages(listOf(d.conversationId), [incoming]))

        if (activeConvId.value === d.conversationId) {
          void markReadIfNeeded(d.conversationId)
        } else {
          const conv = conversations.value.find((c) => c.id === d.conversationId)
          touchConversation(d.conversationId, {
            latestSeq: d.seq,
            lastMsg: d.content,
            bumpTime: true,
            unreadCount: (conv?.unreadCount ?? 0) + 1,
          })
        }
        // 新会话（比如别人刚建的单聊）需要重新拉列表
        if (!conversations.value.some((c) => c.id === d.conversationId)) {
          void loadConversations()
        }
        return
      }

      case 'read_push': {
        const d = env.data as WsReadPushData
        const conv = conversations.value.find((c) => c.id === d.conversationId)
        if (conv) {
          // 记录对方的已读位置（当前 UI 用「已读」标记自己发出的消息是否被看到）
          peerReadSeq.value = { ...peerReadSeq.value, [d.conversationId]: d.readSeq }
        }
        return
      }

      case 'presence': {
        const d = env.data as WsPresenceData
        const next = new Set(onlineUsers.value)
        if (d.online) next.add(d.userId)
        else next.delete(d.userId)
        onlineUsers.value = next
        return
      }

      case 'error': {
        const d = env.data as WsErrorData
        sendingCount.value = Math.max(0, sendingCount.value - 1)
        const clientMsgId = d.reqId !== undefined ? reqToClient.get(d.reqId) : undefined
        if (clientMsgId !== undefined) {
          const convId = findConvByClientMsgId(clientMsgId)
          if (convId !== null) {
            setList(convId, markFailed(listOf(convId), clientMsgId))
          }
          clearAckTimer(clientMsgId)
          reqToClient.delete(d.reqId as string)
        }
        ElMessage.error(d.message || '发送失败')
        return
      }

      default:
        // 未知类型忽略（服务端新增消息类型时前向兼容）
        return
    }
  }

  /** 对方已读位置在状态区已声明（peerReadSeq），这里只做查找。 */
  function findConvByClientMsgId(clientMsgId: string): number | null {
    for (const convId of Object.keys(messages.value)) {
      const id = Number(convId)
      if (listOf(id).some((m) => m.clientMsgId === clientMsgId)) return id
    }
    if (activeConvId.value !== null) return activeConvId.value
    return null
  }

  /** 与某个用户开始单聊（不存在则创建） */
  async function startDirectConversation(peerId: number): Promise<void> {
    const convId = await api.ensureDirectConversation(peerId)
    if (!conversations.value.some((c) => c.id === convId)) {
      await loadConversations()
    }
    await selectConversation(convId)
  }

  /** 登出时清空所有状态 */
  function reset(): void {
    stopSocket()
    conversations.value = []
    messages.value = {}
    activeConvId.value = null
    hasMore.value = {}
    onlineUsers.value = new Set()
    peerReadSeq.value = {}
    for (const t of ackTimers.values()) clearTimeout(t)
    ackTimers.clear()
    reqToClient.clear()
    wsStatus.value = 'idle'
    wsAttempt.value = 0
  }

  return {
    // state
    conversations,
    activeConvId,
    messages,
    loadingConversations,
    loadingMessages,
    hasMore,
    sendingCount,
    wsStatus,
    wsAttempt,
    wsNextDelayMs,
    onlineUsers,
    peerReadSeq,
    // getters
    activeConversation,
    activeMessages,
    isConnected,
    totalUnread,
    // actions
    loadConversations,
    selectConversation,
    loadInitialMessages,
    loadMoreHistory,
    markReadIfNeeded,
    sendMessage,
    retryMessage,
    startSocket,
    stopSocket,
    syncAllConversations,
    startDirectConversation,
    reset,
  }
})
