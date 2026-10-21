/**
 * 前端 API 层 ↔ 真实后端的契约测试。
 *
 * 为什么需要它（而不是只做纯单元测试）：
 *   单元测试能证明「逻辑自己是对的」，但证明不了「我调用的字段名/路径/语义
 *   和后端真的一样」。类型是编译期的，后端返回的 JSON 是运行期的，
 *   两者对不上时 TS 不会报错 —— 只有真跑一次才发现。
 *
 * 这个测试直接复用生产代码（src/api/*），请求真实的 Go 服务。
 * 后端没起时**明确 skip**，不会伪装通过。
 */
import { afterAll, describe, expect, it } from 'vitest'

import * as api from './index'
import { ApiError } from './client'
import { clearSession, saveSession } from './token'

const BACKEND = 'http://127.0.0.1:8080'
const REAL_FETCH = globalThis.fetch

/** 给 api client 的相对路径补上后端 origin（Node 环境没有页面 origin）。 */
function installFetchShim(): void {
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    if (typeof input === 'string' && input.startsWith('/')) {
      return REAL_FETCH(BACKEND + input, init)
    }
    return REAL_FETCH(input as RequestInfo, init)
  }) as typeof fetch
}

installFetchShim()

/**
 * 后端探测放在**模块顶层**（top-level await）。
 *
 * 为什么不能在 beforeAll 里做：跳不跳过得在「收集测试」阶段就决定，
 * 而 beforeAll 跑得比收集晚。顶层 await 让 backendUp 在 describe 之前就有值，
 * 于是可以直接用 it.skip 真正跳过 —— 在 CI 报告里会显示 SKIP，
 * 而不是用一个 return 伪装成 PASS。
 */
const backendUp = await (async (): Promise<boolean> => {
  try {
    const res = await REAL_FETCH(`${BACKEND}/healthz`)
    const body = (await res.json()) as { data?: { status?: string } }
    return body.data?.status === 'ok'
  } catch {
    return false
  }
})()

if (!backendUp) {
  console.warn('⚠ 后端未启动（127.0.0.1:8080），契约测试将全部 SKIP')
}

/** 后端不可用时真正 skip。 */
const itBackend = backendUp ? it : it.skip

const suffix = Math.random().toString(36).slice(2, 8)
const userA = { username: `fe_a_${suffix}`, password: 'Test1234', nickname: `前端甲${suffix}` }
const userB = { username: `fe_b_${suffix}`, password: 'Test1234', nickname: `前端乙${suffix}` }
let idB = 0

afterAll(() => {
  globalThis.fetch = REAL_FETCH
  clearSession()
})

describe('与真实后端的契约一致性', () => {
  itBackend('注册 → 登录 → /users/me 全链路字段对得上', async () => {
    const regA = await api.register(userA)
    expect(typeof regA.userId).toBe('number')
    expect(regA.userId).toBeGreaterThan(0)

    const regB = await api.register(userB)
    idB = regB.userId
    expect(idB).toBeGreaterThan(0)

    // 登录
    const auth = await api.login({ username: userA.username, password: userA.password })
    expect(auth.accessToken).toBeTruthy()
    expect(auth.refreshToken).toBeTruthy()
    expect(auth.user.username).toBe(userA.username)
    // 契约要求 nickname 回传
    expect(auth.user.nickname).toBe(userA.nickname)

    // 把会话存进 localStorage（模拟真实登录后的行为）
    saveSession(auth.accessToken, auth.refreshToken, {
      id: auth.user.id,
      username: auth.user.username,
      nickname: auth.user.nickname,
      avatarUrl: auth.user.avatarUrl,
    })

    // 带 token 访问 /users/me
    const me = await api.me()
    expect(me.id).toBe(auth.user.id)
    expect(me.username).toBe(userA.username)
  })

  itBackend('创建单聊是幂等的（重复调用返回同一个 conversationId）', async () => {
    const first = await api.ensureDirectConversation(idB)
    expect(first).toBeGreaterThan(0)

    const second = await api.ensureDirectConversation(idB)
    expect(second).toBe(first)

    const third = await api.createConversation({ type: 1, memberIds: [idB] })
    expect(third.conversationId).toBe(first)
  })

  itBackend('会话列表的字段与契约完全一致（含未读数与标题解析）', async () => {
    const page = await api.listConversations()
    expect(Array.isArray(page.items)).toBe(true)
    expect(typeof page.total).toBe('number')

    const conv = page.items.find((c) => c.title === userB.nickname)
    expect(conv, '应当能找到与 userB 的单聊会话').toBeTruthy()
    // 逐个字段核对契约（任何一个名字写错这里都会炸）
    expect(typeof conv?.id).toBe('number')
    expect(conv?.type).toBe(1)
    expect(typeof conv?.lastMsg).toBe('string')
    expect(typeof conv?.lastMsgAt).toBe('number')
    expect(typeof conv?.latestSeq).toBe('number')
    expect(typeof conv?.myLastReadSeq).toBe('number')
    expect(conv?.unreadCount).toBe(0)
  })

  itBackend('历史消息与 sync 的空状态语义正确', async () => {
    const page = await api.listConversations()
    const conv = page.items.find((c) => c.title === userB.nickname)
    expect(conv).toBeTruthy()
    const convId = conv?.id as number

    const history = await api.listMessages(convId, 0, 30)
    expect(history.items).toEqual([])
    expect(history.hasMore).toBe(false)

    const sync = await api.syncMessages(convId, 0)
    expect(sync.items).toEqual([])
    expect(sync.latestSeq).toBe(0)
    expect(sync.hasGap).toBe(false)
  })

  itBackend('成员列表返回双方，字段齐全', async () => {
    const page = await api.listConversations()
    const conv = page.items.find((c) => c.title === userB.nickname)
    const members = await api.listMembers(conv?.id as number)
    expect(members.total).toBe(2)
    const ids = members.items.map((m) => m.userId).sort()
    expect(ids).toHaveLength(2)
    for (const m of members.items) {
      expect(typeof m.userId).toBe('number')
      expect(typeof m.username).toBe('string')
      expect(typeof m.nickname).toBe('string')
      expect(typeof m.lastReadSeq).toBe('number')
    }
  })

  itBackend('参数校验：readSeq=0 被后端拒绝，且前端的 ApiError 能带出业务码', async () => {
    const page = await api.listConversations()
    const conv = page.items.find((c) => c.title === userB.nickname)

    await expect(api.markRead(conv?.id as number, 0)).rejects.toBeInstanceOf(ApiError)
    try {
      await api.markRead(conv?.id as number, 0)
    } catch (e) {
      const err = e as ApiError
      // 契约 §1.2：参数错误是 1001
      expect(err.code).toBe(1001)
    }
  })

  itBackend('非成员访问会话消息被拒绝（403 / 3002）', async () => {
    // 用 userB 登录，建一个 userB 不在其中的会话是不可能的（他没有别的会话），
    // 所以这里换一种方式：新注册一个用户，用不存在的会话 id 访问
    const intruder = {
      username: `fe_x_${suffix}`,
      password: 'Test1234',
      nickname: `路人${suffix}`,
    }
    await api.register(intruder)
    const auth = await api.login({ username: intruder.username, password: intruder.password })
    saveSession(auth.accessToken, auth.refreshToken, {
      id: auth.user.id,
      username: auth.user.username,
      nickname: auth.user.nickname,
      avatarUrl: auth.user.avatarUrl,
    })

    const page = await api.listConversations()
    expect(page.items).toHaveLength(0)

    await expect(api.listMessages(1, 0, 10)).rejects.toBeInstanceOf(ApiError)

    // 恢复 userA 的会话，供后续（如有）使用
    const back = await api.login({ username: userA.username, password: userA.password })
    saveSession(back.accessToken, back.refreshToken, {
      id: back.user.id,
      username: back.user.username,
      nickname: back.user.nickname,
      avatarUrl: back.user.avatarUrl,
    })
  })

  itBackend('错误密码登录返回 2002（前端能据此给出准确提示）', async () => {
    try {
      await api.login({ username: userA.username, password: 'WrongPass1' })
      throw new Error('应当登录失败')
    } catch (e) {
      const err = e as ApiError
      expect(err.code).toBe(2002)
      expect(err.status).toBe(401)
    }
  })
})

// ===========================================================================
// Sprint 5：群管理接口的契约验证
// ===========================================================================
describe('群管理接口（Sprint 5）', () => {
  let groupId = 0
  let userCId = 0
  const userC = {
    username: `fe_c_${suffix}`,
    password: 'Test1234',
    nickname: `前端丙${suffix}`,
  }

  /** 让当前会话成为某个用户 */
  async function loginAs(u: { username: string; password: string }): Promise<number> {
    const auth = await api.login({ username: u.username, password: u.password })
    saveSession(auth.accessToken, auth.refreshToken, {
      id: auth.user.id,
      username: auth.user.username,
      nickname: auth.user.nickname,
      avatarUrl: auth.user.avatarUrl,
    })
    return auth.user.id
  }

  itBackend('建群后会话列表能拿到 type=2 与 ownerId', async () => {
    await loginAs(userA) // 确保是 userA 的身份
    const me = await api.me()

    // 先注册第三个用户备用
    const regC = await api.register(userC)
    userCId = regC.userId

    const res = await api.createConversation({
      type: 2,
      title: `契约测试群${suffix}`,
      memberIds: [idB],
    })
    groupId = res.conversationId
    expect(groupId).toBeGreaterThan(0)

    const page = await api.listConversations()
    const group = page.items.find((c) => c.id === groupId)
    expect(group, '会话列表里应能看到刚建的群').toBeTruthy()
    expect(group?.type).toBe(2)
    // 契约新增字段：ownerId
    expect(group?.ownerId).toBe(me.id)
    expect(group?.title).toBe(`契约测试群${suffix}`)
    expect(group?.latestSeq).toBe(0)
    expect(group?.unreadCount).toBe(0)
  })

  itBackend('拉人入群：新增数正确，已在群里的忽略（幂等）', async () => {
    await loginAs(userA)

    // 拉 userC 进群
    const first = await api.addMembers(groupId, [userCId])
    expect(first.added).toBe(1)

    // 再拉一次同一个 —— 幂等，added=0
    const second = await api.addMembers(groupId, [userCId])
    expect(second.added).toBe(0)

    const members = await api.listMembers(groupId)
    expect(members.total).toBe(3) // owner + idB + userC
    const ids = members.items.map((m) => m.userId)
    expect(ids).toContain(userCId)
    // 每个人的 lastReadSeq 都要是数字（前端靠它算「已读 N/M」）
    for (const m of members.items) {
      expect(typeof m.lastReadSeq).toBe('number')
    }
  })

  itBackend('非成员不能拉人（3002）', async () => {
    // 用 userC 之外的第四个用户尝试（复用 fe_x 那个「路人」）
    const outsider = { username: `fe_x_${suffix}`, password: 'Test1234' }
    await loginAs(outsider)
    try {
      await api.addMembers(groupId, [userCId])
      throw new Error('非成员拉人应当被拒绝')
    } catch (e) {
      const err = e as ApiError
      expect(err.code).toBe(3002)
    }
  })

  itBackend('单聊不能拉人（1001）', async () => {
    await loginAs(userA)
    const dmId = await api.ensureDirectConversation(idB)
    try {
      await api.addMembers(dmId, [userCId])
      throw new Error('单聊拉人应当被拒绝')
    } catch (e) {
      const err = e as ApiError
      expect(err.code).toBe(1001)
    }
  })

  itBackend('成员可以退群，退群后不在成员列表里', async () => {
    await loginAs(userC)
    // 退群前：userC 能看到这个群
    let page = await api.listConversations()
    expect(page.items.some((c) => c.id === groupId)).toBe(true)

    await api.leaveGroup(groupId)

    // 退群后：会话列表里没有它了
    page = await api.listConversations()
    expect(page.items.some((c) => c.id === groupId)).toBe(false)
    // 也读不到消息了
    await expect(api.listMessages(groupId, 0, 10)).rejects.toBeInstanceOf(ApiError)
  })

  itBackend('退群是幂等的（重复退群不报错）', async () => {
    await loginAs(userC)
    // 已经退过了，再退一次仍然成功
    await expect(api.leaveGroup(groupId)).resolves.toBeDefined()
  })

  itBackend('群主不能退群（1001）', async () => {
    await loginAs(userA)
    try {
      await api.leaveGroup(groupId)
      throw new Error('群主退群应当被拒绝')
    } catch (e) {
      const err = e as ApiError
      expect(err.code).toBe(1001)
      expect(err.message).toContain('群主')
    }
  })
})
