import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  HEARTBEAT_INTERVAL_MS,
  WsClient,
  buildWsUrl,
  type WebSocketLike,
  type WsStatus,
  type WsStatusInfo,
} from './client'

/** 假的 WebSocket：手动触发事件，用来精确验证客户端的状态机。 */
class FakeSocket implements WebSocketLike {
  readyState = 0
  sent: string[] = []
  closed = false
  onopen: ((ev: unknown) => void) | null = null
  onclose: ((ev: { code: number; reason: string }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null

  constructor(public readonly url: string) {}

  send(data: string): void {
    this.sent.push(data)
  }

  close(): void {
    this.closed = true
    this.readyState = 3
  }

  // ---- 测试驱动 ----
  fireOpen(): void {
    this.readyState = 1
    this.onopen?.({})
  }
  fireClose(code = 1006): void {
    this.readyState = 3
    this.onclose?.({ code, reason: '' })
  }
  fireError(): void {
    this.onerror?.({})
  }
  fireMessage(obj: unknown): void {
    this.onmessage?.({ data: JSON.stringify(obj) })
  }
  pings(): number {
    return this.sent.filter((s) => s.includes('"ping"')).length
  }
}

interface Rig {
  client: WsClient
  sockets: FakeSocket[]
  statuses: Array<{ status: WsStatus; info: WsStatusInfo }>
  messages: unknown[]
  reconnects: number
}

function makeRig(opts: { token?: string } = {}): Rig {
  const sockets: FakeSocket[] = []
  const statuses: Array<{ status: WsStatus; info: WsStatusInfo }> = []
  const messages: unknown[] = []
  let reconnects = 0
  const token = opts.token ?? 'fake-token'

  const client = new WsClient({
    getToken: () => token,
    onMessage: (env) => messages.push(env),
    onStatus: (status, info) => statuses.push({ status, info }),
    onReconnected: () => {
      reconnects += 1
    },
    socketFactory: (url) => {
      const s = new FakeSocket(url)
      sockets.push(s)
      return s
    },
    // 固定 0.5 → 抖动抵消，退避时间等于基准值，便于断言
    random: () => 0.5,
  })

  return {
    client,
    sockets,
    statuses,
    messages,
    get reconnects() {
      return reconnects
    },
  } as Rig
}

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('buildWsUrl', () => {
  it('普通页面用 ws://，https 页面用 wss://', () => {
    expect(buildWsUrl('abc', '127.0.0.1:8080')).toBe('ws://127.0.0.1:8080/ws?token=abc')
  })

  it('token 会被 URL 编码（避免特殊字符破坏 query）', () => {
    const url = buildWsUrl('a+b/c=', 'h:1')
    expect(url).toContain('token=a%2Bb%2Fc%3D')
  })
})

describe('连接与状态流转', () => {
  it('首次连接：connecting → open', () => {
    const rig = makeRig()
    rig.client.connect()
    expect(rig.sockets).toHaveLength(1)
    expect(rig.client.getStatus()).toBe('connecting')

    rig.sockets[0]?.fireOpen()
    expect(rig.client.getStatus()).toBe('open')
  })

  it('首次连接成功不触发 onReconnected（只有重连才触发补拉）', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()
    expect(rig.reconnects).toBe(0)
  })

  it('没有 token 时不建连接，直接 closed', () => {
    const rig = makeRig({ token: '' })
    rig.client.connect()
    expect(rig.sockets).toHaveLength(0)
    expect(rig.client.getStatus()).toBe('closed')
  })
})

describe('断线重连', () => {
  it('异常断开进入 reconnecting，并给出第 1 次退避 1000ms', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()

    rig.sockets[0]?.fireClose(1006)

    expect(rig.client.getStatus()).toBe('reconnecting')
    const last = rig.statuses[rig.statuses.length - 1]
    expect(last?.info.attempt).toBe(1)
    expect(last?.info.nextDelayMs).toBe(1000)
    expect(last?.info.closeCode).toBe(1006)
  })

  it('等够退避时间后自动发起新连接，成功后触发 onReconnected', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()
    rig.sockets[0]?.fireClose()

    // 还没到时间：不该有新连接
    vi.advanceTimersByTime(999)
    expect(rig.sockets).toHaveLength(1)

    vi.advanceTimersByTime(1)
    expect(rig.sockets).toHaveLength(2)

    rig.sockets[1]?.fireOpen()
    expect(rig.client.getStatus()).toBe('open')
    expect(rig.reconnects).toBe(1)
  })

  it('连续失败时退避翻倍：1000 → 2000 → 4000', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()

    rig.sockets[0]?.fireClose()
    expect(rig.statuses[rig.statuses.length - 1]?.info.nextDelayMs).toBe(1000)

    vi.advanceTimersByTime(1000)
    expect(rig.sockets).toHaveLength(2)

    // 第二次也失败（没 open 就 close）
    rig.sockets[1]?.fireClose()
    expect(rig.statuses[rig.statuses.length - 1]?.info.nextDelayMs).toBe(2000)

    vi.advanceTimersByTime(2000)
    expect(rig.sockets).toHaveLength(3)

    rig.sockets[2]?.fireClose()
    expect(rig.statuses[rig.statuses.length - 1]?.info.nextDelayMs).toBe(4000)
  })

  it('重连成功后 attempt 归零（再次断线仍从 1 秒开始）', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()
    rig.sockets[0]?.fireClose()
    vi.advanceTimersByTime(1000)
    rig.sockets[1]?.fireOpen()

    expect(rig.client.getAttempt()).toBe(0)

    rig.sockets[1]?.fireClose()
    expect(rig.statuses[rig.statuses.length - 1]?.info.nextDelayMs).toBe(1000)
  })

  it('⚠ onerror 不触发重连（重连的唯一入口是 onclose，否则会连出两条）', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()

    const before = rig.sockets.length
    rig.sockets[0]?.fireError()

    // 只报错、不置状态、不建新连接
    expect(rig.sockets).toHaveLength(before)
    expect(rig.client.getStatus()).toBe('open')

    // 随后的 onclose 才触发重连
    rig.sockets[0]?.fireClose()
    expect(rig.client.getStatus()).toBe('reconnecting')
  })

  it('主动关闭后不再重连（登出场景）', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()

    rig.client.close()
    expect(rig.client.getStatus()).toBe('closed')

    // 关闭会触发 onclose，但不该安排重连
    rig.sockets[0]?.fireClose(1000)
    vi.advanceTimersByTime(60_000)
    expect(rig.sockets).toHaveLength(1)
    expect(rig.client.getStatus()).toBe('closed')
  })
})

describe('心跳', () => {
  it('连接建立后每 25 秒发一次 ping', () => {
    const rig = makeRig()
    rig.client.connect()
    const s = rig.sockets[0]
    s?.fireOpen()

    expect(s?.pings()).toBe(0)
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS)
    expect(s?.pings()).toBe(1)
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS)
    expect(s?.pings()).toBe(2)
  })

  it('断开后停止心跳（否则会往死连接里塞消息）', () => {
    const rig = makeRig()
    rig.client.connect()
    const s = rig.sockets[0]
    s?.fireOpen()
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS)
    const before = s?.pings() ?? 0

    s?.fireClose()
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS * 3)
    expect(s?.pings()).toBe(before)
  })
})

describe('收发', () => {
  it('收到合法 JSON 消息时回调 onMessage', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()

    rig.sockets[0]?.fireMessage({ type: 'push', data: { seq: 1 } })
    expect(rig.messages).toHaveLength(1)
    expect(rig.messages[0]).toEqual({ type: 'push', data: { seq: 1 } })
  })

  it('收到非法 JSON 时忽略，不抛异常、不影响连接', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()

    expect(() => rig.sockets[0]?.onmessage?.({ data: '{{{不是JSON' })).not.toThrow()
    expect(rig.messages).toHaveLength(0)
    expect(rig.client.getStatus()).toBe('open')
  })

  it('非字符串帧（二进制）直接忽略', () => {
    const rig = makeRig()
    rig.client.connect()
    rig.sockets[0]?.fireOpen()
    rig.sockets[0]?.onmessage?.({ data: new ArrayBuffer(4) })
    expect(rig.messages).toHaveLength(0)
  })

  it('未连接时 send 返回 false（调用方据此标记失败可重试）', () => {
    const rig = makeRig()
    rig.client.connect()
    // 还没 open
    expect(rig.client.send({ type: 'ping' })).toBe(false)

    rig.sockets[0]?.fireOpen()
    expect(rig.client.send({ type: 'ping' })).toBe(true)
  })
})
