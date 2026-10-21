import {
  ApiCode,
  type ApiResponse,
} from './types'
import {
  clearSession,
  getAccessToken,
  getRefreshToken,
  saveAccessToken,
} from './token'

/** 接口基址。
 *
 * 用相对路径 `/api/v1`：
 *   - 开发时由 Vite 的 proxy 转到 127.0.0.1:8080（同源，无 CORS）
 *   - 生产时由 Nginx 反代到 Go 服务（同样是同源）
 * 前端代码在两种环境下**一字不改**。
 */
const BASE = '/api/v1'

/** 业务错误。带 code 让调用方能按错误码分支处理。 */
export class ApiError extends Error {
  constructor(
    public readonly code: number,
    message: string,
    public readonly status: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/** 登录态彻底失效时的回调（由 auth store 注册，用于清状态 + 跳登录页）。 */
let authLostHandler: (() => void) | null = null
export function setAuthLostHandler(fn: () => void): void {
  authLostHandler = fn
}

/**
 * 单飞（single-flight）刷新。
 *
 * 为什么必须单飞：页面打开时可能同时发出 5 个请求，如果 access token 过期，
 * 5 个请求会同时 401、同时调 /auth/refresh。而 refresh token 是一次性的
 * （后端登出/轮换时旧的可能失效），并发刷新会互相踩，导致其中几个失败、
 * 用户被莫名其妙踢下线。
 *
 * 做法：把并发的刷新请求合并成一个 in-flight Promise，所有等待者共享结果。
 */
let refreshPromise: Promise<boolean> | null = null

async function doRefresh(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  try {
    const res = await fetch(`${BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken }),
    })
    if (!res.ok) return false

    const body = (await res.json()) as ApiResponse<{ accessToken: string }>
    if (body.code !== ApiCode.OK || !body.data?.accessToken) return false

    saveAccessToken(body.data.accessToken)
    return true
  } catch {
    // 网络错误也当作刷新失败：宁可让用户重新登录，也不要用一个确定过期的 token 死循环重试
    return false
  }
}

function refreshOnce(): Promise<boolean> {
  if (refreshPromise === null) {
    refreshPromise = doRefresh().finally(() => {
      refreshPromise = null
    })
  }
  return refreshPromise
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  /** 会被 JSON.stringify */
  body?: unknown
  /** 是否参与 401 自动刷新重试（默认 true） */
  retryOnUnauthorized?: boolean
}

/**
 * 统一请求入口。
 *
 * 行为：
 *   1. 自动带 Authorization（有 token 时）
 *   2. 解析统一响应包装，code !== 0 一律抛 ApiError
 *   3. HTTP 401 时尝试刷新一次 token 并重放请求（只重放一次，避免死循环）
 *   4. 刷新也失败 → 清空会话 + 触发 authLost（跳登录页）
 */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, retryOnUnauthorized = true } = opts

  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const token = getAccessToken()
  if (token) headers['Authorization'] = `Bearer ${token}`

  let res: Response
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (e) {
    // 网络层失败（断网、服务没起）—— 与业务错误区分开，便于 UI 给出不同提示
    throw new ApiError(-1, `网络请求失败：${(e as Error).message}`, 0)
  }

  // ---- 401：刷新后重放一次 ----
  if (res.status === 401 && retryOnUnauthorized) {
    const refreshed = await refreshOnce()
    if (refreshed) {
      return request<T>(path, { ...opts, retryOnUnauthorized: false })
    }
    clearSession()
    authLostHandler?.()
    throw new ApiError(ApiCode.UNAUTHORIZED, '登录已过期，请重新登录', 401)
  }

  // ---- 解析统一响应包装 ----
  let payload: ApiResponse<T>
  try {
    payload = (await res.json()) as ApiResponse<T>
  } catch {
    throw new ApiError(ApiCode.INTERNAL, `响应不是合法 JSON（HTTP ${res.status}）`, res.status)
  }

  if (payload.code !== ApiCode.OK) {
    throw new ApiError(payload.code, payload.message || '请求失败', res.status)
  }
  return payload.data as T
}

/** 便捷方法 */
export const http = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown, retry = true) =>
    request<T>(path, { method: 'POST', body, retryOnUnauthorized: retry }),
  del: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
