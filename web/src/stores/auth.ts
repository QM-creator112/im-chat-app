import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import * as api from '@/api'
import { setAuthLostHandler } from '@/api/client'
import {
  clearSession,
  getAccessToken,
  getStoredUser,
  saveSession,
  type StoredUser,
} from '@/api/token'

/**
 * 登录态。
 *
 * 注意 accessToken 用 ref 而不是每次读 localStorage：
 * localStorage 不是响应式的，直接用会导致「token 变了但界面没反应」。
 */
export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref<string>(getAccessToken())
  const user = ref<StoredUser | null>(getStoredUser())
  const loading = ref(false)
  const error = ref('')

  const isLoggedIn = computed(() => accessToken.value !== '' && user.value !== null)
  const displayName = computed(() => user.value?.nickname || user.value?.username || '')

  /** 把持久化的会话读进 ref（页面刷新后调用） */
  function hydrate(): void {
    accessToken.value = getAccessToken()
    user.value = getStoredUser()
  }

  async function login(username: string, password: string): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api.login({ username, password })
      saveSession(res.accessToken, res.refreshToken, {
        id: res.user.id,
        username: res.user.username,
        nickname: res.user.nickname,
        avatarUrl: res.user.avatarUrl,
      })
      accessToken.value = res.accessToken
      user.value = {
        id: res.user.id,
        username: res.user.username,
        nickname: res.user.nickname,
        avatarUrl: res.user.avatarUrl,
      }
    } catch (e) {
      error.value = (e as Error).message
      throw e
    } finally {
      loading.value = false
    }
  }

  async function register(username: string, password: string, nickname?: string): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      await api.register({ username, password, nickname })
      await login(username, password)
    } catch (e) {
      error.value = (e as Error).message
      throw e
    } finally {
      loading.value = false
    }
  }

  async function logout(): Promise<void> {
    const rt = localStorage.getItem('im.refreshToken') ?? ''
    try {
      if (rt) await api.logout(rt)
    } catch {
      // 登出接口失败也要清本地——否则用户会卡在「点了登出但还登录着」
    }
    reset()
  }

  /** 清空本地登录态（登出 / token 彻底失效时） */
  function reset(): void {
    clearSession()
    accessToken.value = ''
    user.value = null
  }

  // api client 在刷新令牌也失败时会调这个
  setAuthLostHandler(() => {
    reset()
  })

  return {
    accessToken,
    user,
    loading,
    error,
    isLoggedIn,
    displayName,
    hydrate,
    login,
    register,
    logout,
    reset,
  }
})
