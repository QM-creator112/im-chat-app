/**
 * 接口类型定义。
 *
 * ⚠️ 这些类型是 docs/api.md（已冻结的契约）的 TypeScript 镜像。
 * 任何字段改动都必须先改契约文档，再改这里 —— 顺序不能反。
 */

/** 统一响应包装（契约 §1.1） */
export interface ApiResponse<T> {
  code: number
  message: string
  data?: T
}

/** 业务错误码（契约 §1.2，与后端 apicode 包一一对应） */
export const ApiCode = {
  OK: 0,
  BAD_PARAM: 1001,
  UNAUTHORIZED: 1002,
  FORBIDDEN: 1003,
  USER_EXISTS: 2001,
  BAD_CREDENTIAL: 2002,
  BAD_REFRESH: 2003,
  CONV_NOT_FOUND: 3001,
  NOT_MEMBER: 3002,
  PROCESSING: 4001,
  INTERNAL: 5000,
} as const

/** 会话类型 */
export const ConversationType = {
  DIRECT: 1,
  GROUP: 2,
} as const

/** 消息内容类型 */
export const ContentType = {
  TEXT: 1,
  IMAGE: 2,
  FILE: 3,
  VOICE: 4,
  SYSTEM: 5,
} as const

export interface User {
  id: number
  username: string
  nickname: string
  avatarUrl: string
  createdAt?: string
  updatedAt?: string
}

export interface Conversation {
  id: number
  type: number
  title: string
  lastMsg: string
  /** Unix 毫秒 */
  lastMsgAt: number
  latestSeq: number
  myLastReadSeq: number
  unreadCount: number
}

export interface Message {
  id: number
  conversationId: number
  /** 会话内单调递增（不保证连续） */
  seq: number
  senderId: number
  senderName?: string
  clientMsgId: string
  contentType: number
  content: string
  /** Unix 毫秒 */
  createdAt: number
}

export interface Member {
  userId: number
  username: string
  nickname: string
  avatarUrl: string
  lastReadSeq: number
}

/** 分页列表（契约 §1.3） */
export interface Page<T> {
  items: T[]
  total: number
  nextCursor?: string
}

/** 历史消息分页结果 */
export interface MessagePage {
  items: Message[]
  hasMore: boolean
}

/** 增量补拉结果（契约 §2.3） */
export interface SyncResult {
  items: Message[]
  latestSeq: number
  /** true 表示增量已不可用，客户端应改为「重新拉最近 N 条」 */
  hasGap: boolean
}

export interface AuthResult {
  accessToken: string
  refreshToken: string
  user: User
}

/** WebSocket 消息信封（契约 §3.2） */
export interface WsEnvelope<T = unknown> {
  type: string
  reqId?: string
  data?: T
}

export interface WsSendData {
  clientMsgId: string
  conversationId: number
  contentType: number
  content: string
}

export interface WsSendAckData {
  clientMsgId: string
  seq: number
  createdAt: number
}

export interface WsPushData {
  conversationId: number
  seq: number
  senderId: number
  senderName?: string
  contentType: number
  content: string
  createdAt: number
}

export interface WsReadPushData {
  conversationId: number
  userId: number
  readSeq: number
}

export interface WsPresenceData {
  userId: number
  online: boolean
}

export interface WsErrorData {
  code: number
  message: string
  reqId?: string
}
