import type { WsEnvelope } from '@/api/types'
import { computeBackoff } from '@/utils/backoff'

/**
 * 连接状态。对应原型第 ⑤ 屏（断线重连）的三种 UI 表现：
 *   idle / connecting   → 首次连接中
 *   open                → 「● 已连接」
 *   reconnecting        → 「◌ 已断开，正在重连…（第 N 次）」
 *   closed              → 主动关闭（登出）
 */
export type WsStatus = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface WsStatusInfo {
  /** 第几次重连尝试（0 = 首次连接） */
  attempt: number
  /** 下次重连还要等多久（毫秒），仅 reconnecting 时有值 */
  nextDelayMs?: number
  /** 关闭码，便于排查 */
  closeCode?: number
}

/** 心跳间隔，必须小于服务端的读超时（60s）。服务端 ping 是 25s，这里对齐。 */
export const HEARTBEAT_INTERVAL_MS = 25_000

/**
 * 最小 WebSocket 接口。
 *
 * 抽出来是为了**可测试**：单测里注入一个假的 socket，
 * 手动触发 open/close/message 事件，就能验证退避、重连时机、心跳这些逻辑，
 * 不需要真的起一个浏览器。
 */
export interface WebSocketLike {
  readyState: number
  send(data: string): void
  close(code?: number, reason?: string): void
  onopen: ((ev: unknown) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: ((ev: unknown) => void) | null
  onmessage: ((ev: { data: unknown }) => void) | null
}

export interface WsClientOptions {
  /** 取当前 access token（每次连接时重新取，支持 token 刷新后重连） */
  getToken: () => string
  /** 收到一条服务端消息 */
  onMessage: (env: WsEnvelope) => void
  /** 状态变化 */
  onStatus: (status: WsStatus, info: WsStatusInfo) => void
  /** 重连成功（首次连接不算）。用于触发逐会话 /sync 补拉。 */
  onReconnected: () => void
  /** 注入 socket 工厂（测试用） */
  socketFactory?: (url: string) => WebSocketLike
  /** 注入随机源（测试用，让退避抖动可预测） */
  random?: () => number
}

/** 拼接 WebSocket 地址（自动适配 http/https → ws/wss）。 */
export function buildWsUrl(token: string, origin?: string): string {
  const base = origin ?? (typeof location !== 'undefined' ? location.host : '127.0.0.1:8080')
  const isSecure =
    typeof location !== 'undefined' ? location.protocol === 'https:' : false
  const proto = isSecure ? 'wss:' : 'ws:'
  return `${proto}//${base}/ws?token=${encodeURIComponent(token)}`
}

export class WsClient {
  private ws: WebSocketLike | null = null
  private status: WsStatus = 'idle'
  private attempt = 0
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  /** 主动关闭标记：置位后不再自动重连（登出场景） */
  private manualClose = false
  /** 是否有过至少一次成功连接（用于区分「首次连接」与「重连」） */
  private everOpened = false

  constructor(private readonly opts: WsClientOptions) {}

  getStatus(): WsStatus {
    return this.status
  }

  getAttempt(): number {
    return this.attempt
  }

  /** 建立连接（或重连）。重复调用时先清掉旧连接与定时器。 */
  connect(): void {
    this.manualClose = false
    this.clearReconnectTimer()

    const token = this.opts.getToken()
    if (!token) {
      this.setStatus('closed', { attempt: this.attempt })
      return
    }

    this.setStatus(this.everOpened ? 'reconnecting' : 'connecting', {
      attempt: this.attempt,
    })

    const factory =
      this.opts.socketFactory ??
      ((url: string) => new WebSocket(url) as unknown as WebSocketLike)

    let socket: WebSocketLike
    try {
      socket = factory(buildWsUrl(token))
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws = socket

    socket.onopen = () => {
      this.attempt = 0
      const wasReconnect = this.everOpened
      this.everOpened = true
      this.setStatus('open', { attempt: 0 })
      this.startHeartbeat()
      // 首次连接不触发补拉（刚登录，页面本来就会拉一次会话列表）
      if (wasReconnect) this.opts.onReconnected()
    }

    socket.onmessage = (ev) => {
      if (typeof ev.data !== 'string') return
      try {
        const env = JSON.parse(ev.data) as WsEnvelope
        this.opts.onMessage(env)
      } catch {
        // 单条消息格式错不该影响连接，忽略即可
      }
    }

    // ⚠️ onerror 里**不做任何处理**。
    // 浏览器的 WebSocket 在出错后一定会再触发 onclose，
    // 如果两个回调里都去重连，就会连出两条连接（互相干扰、连接数翻倍）。
    // 重连的唯一入口是 onclose。
    socket.onerror = () => {}

    socket.onclose = (ev) => {
      this.stopHeartbeat()
      if (this.manualClose) {
        this.setStatus('closed', { attempt: this.attempt, closeCode: ev.code })
        return
      }
      this.scheduleReconnect(ev.code)
    }
  }

  /**
   * 发送一条消息。
   * @returns true = 已交给 socket；false = 当前不可发（调用方应进入失败态并允许重试）
   */
  send(env: WsEnvelope): boolean {
    if (!this.ws || this.ws.readyState !== 1 /* OPEN */) return false
    try {
      this.ws.send(JSON.stringify(env))
      return true
    } catch {
      return false
    }
  }

  /** 主动关闭（登出 / 页面卸载）。不会触发自动重连。 */
  close(): void {
    this.manualClose = true
    this.clearReconnectTimer()
    this.stopHeartbeat()
    if (this.ws) {
      try {
        this.ws.close(1000, 'client closed')
      } catch {
        // 已关闭则忽略
      }
      this.ws = null
    }
    this.setStatus('closed', { attempt: this.attempt })
  }

  // ---------------- 内部 ----------------

  private setStatus(status: WsStatus, info: WsStatusInfo): void {
    this.status = status
    this.opts.onStatus(status, info)
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      this.send({ type: 'ping' })
    }, HEARTBEAT_INTERVAL_MS)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }

  private scheduleReconnect(closeCode?: number): void {
    this.attempt += 1
    const delay = computeBackoff(this.attempt, this.opts.random)
    this.setStatus('reconnecting', {
      attempt: this.attempt,
      nextDelayMs: delay,
      closeCode,
    })

    this.clearReconnectTimer()
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      if (!this.manualClose) this.connect()
    }, delay)
  }
}
