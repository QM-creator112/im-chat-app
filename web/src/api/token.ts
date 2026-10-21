/**
 * 令牌的本地持久化。
 *
 * 单独一个模块是为了避免循环依赖：
 * api/client、ws/client、stores/auth 都需要读写令牌，
 * 如果都从 stores/auth 里取，会绕成一团环。
 *
 * 存储位置的选择（面试会被问）：
 *   - localStorage：刷新页面不掉登录，但**能被 XSS 读到**
 *   - httpOnly Cookie：XSS 读不到，但要处理 CSRF，且跨端口开发麻烦
 * 本项目是个人作品集、无同源第三方脚本，选 localStorage 并在设计里明确这个取舍。
 */

const KEY_ACCESS = 'im.accessToken'
const KEY_REFRESH = 'im.refreshToken'
const KEY_USER = 'im.user'

export interface StoredUser {
  id: number
  username: string
  nickname: string
  avatarUrl: string
}

export function getAccessToken(): string {
  return localStorage.getItem(KEY_ACCESS) ?? ''
}

export function getRefreshToken(): string {
  return localStorage.getItem(KEY_REFRESH) ?? ''
}

export function getStoredUser(): StoredUser | null {
  const raw = localStorage.getItem(KEY_USER)
  if (!raw) return null
  try {
    return JSON.parse(raw) as StoredUser
  } catch {
    return null
  }
}

export function saveSession(accessToken: string, refreshToken: string, user: StoredUser): void {
  localStorage.setItem(KEY_ACCESS, accessToken)
  localStorage.setItem(KEY_REFRESH, refreshToken)
  localStorage.setItem(KEY_USER, JSON.stringify(user))
}

export function saveAccessToken(accessToken: string): void {
  localStorage.setItem(KEY_ACCESS, accessToken)
}

export function clearSession(): void {
  localStorage.removeItem(KEY_ACCESS)
  localStorage.removeItem(KEY_REFRESH)
  localStorage.removeItem(KEY_USER)
}
