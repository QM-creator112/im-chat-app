<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import ConversationList from '@/components/ConversationList.vue'
import MessageList from '@/components/MessageList.vue'
import Composer from '@/components/Composer.vue'
import * as api from '@/api'
import type { User } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { useChatStore } from '@/stores/chat'

const auth = useAuthStore()
const chat = useChatStore()
const router = useRouter()

const showFriendPicker = ref(false)
const friends = ref<User[]>([])
const loadingFriends = ref(false)

const activeTitle = computed(() => chat.activeConversation?.title || '选择一个会话')
const peerOnline = computed<boolean>(() => {
  // 单聊时对方的在线状态（来自 presence 推送）
  const conv = chat.activeConversation
  if (!conv || conv.type !== 1) return false
  for (const uid of chat.onlineUsers) {
    if (uid !== auth.user?.id) return true
  }
  return false
})

const reconnectingText = computed(() => {
  if (chat.wsStatus !== 'reconnecting') return ''
  const delay = chat.wsNextDelayMs ?? 0
  const secs = Math.round(delay / 100) / 10
  return `连接已断开，正在重连（第 ${chat.wsAttempt} 次，${secs} 秒后重试）`
})

onMounted(async () => {
  auth.hydrate()
  if (!auth.isLoggedIn) {
    await router.replace({ name: 'login' })
    return
  }
  chat.startSocket()
  await chat.loadConversations()
})

onBeforeUnmount(() => {
  chat.stopSocket()
})

async function openFriendPicker(): Promise<void> {
  showFriendPicker.value = true
  loadingFriends.value = true
  try {
    const page = await api.listFriends()
    friends.value = page.items
    if (page.items.length === 0) {
      ElMessage.info('还没有好友，先去「加好友」添加一个')
    }
  } catch (e) {
    ElMessage.error(`加载好友失败：${(e as Error).message}`)
  } finally {
    loadingFriends.value = false
  }
}

async function startChatWith(friend: User): Promise<void> {
  showFriendPicker.value = false
  try {
    await chat.startDirectConversation(friend.id)
  } catch (e) {
    ElMessage.error(`创建会话失败：${(e as Error).message}`)
  }
}

async function handleLogout(): Promise<void> {
  chat.reset()
  await auth.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="chat-page">
    <!-- 断线重连横幅：对应原型第 ⑤ 屏 -->
    <div v-if="chat.wsStatus === 'reconnecting'" class="banner">
      ⟳ {{ reconnectingText }}
    </div>
    <div v-else-if="chat.wsStatus === 'closed'" class="banner banner-error">
      连接已关闭，请重新登录
    </div>

    <div class="chat-body">
      <!-- 左侧：我的信息 + 会话列表 -->
      <div class="side">
        <div class="me">
          <div class="me-avatar">{{ (auth.user?.nickname || '?').slice(0, 1) }}</div>
          <div class="me-info">
            <div class="me-name">{{ auth.displayName }}</div>
            <div class="me-status">
              <span class="dot" :class="chat.isConnected ? 'on' : 'off'" />
              {{ chat.isConnected ? '已连接' : '未连接' }}
              <span v-if="chat.totalUnread > 0" class="me-unread">· {{ chat.totalUnread }} 条未读</span>
            </div>
          </div>
          <el-button link type="primary" @click="openFriendPicker">＋</el-button>
        </div>

        <ConversationList />

        <div class="side-footer">
          <el-button link @click="handleLogout">退出登录</el-button>
        </div>
      </div>

      <!-- 右侧：当前会话 -->
      <main class="main">
        <header class="main-header">
          <div>
            <div class="main-title">{{ activeTitle }}</div>
            <div v-if="chat.activeConversation" class="main-sub">
              <span class="dot" :class="peerOnline ? 'on' : 'off'" />
              {{ peerOnline ? '对方在线' : '对方离线' }}
              <span class="sep">·</span>
              最新 seq {{ chat.activeConversation.latestSeq }}
            </div>
          </div>
        </header>

        <el-empty
          v-if="!chat.activeConversation"
          description="选一个会话开始聊天"
          :image-size="100"
          class="main-empty"
        />

        <template v-else>
          <MessageList />
          <Composer />
        </template>
      </main>
    </div>

    <!-- 选择好友开始聊天 -->
    <el-dialog v-model="showFriendPicker" title="选择好友开始聊天" width="380px">
      <el-skeleton v-if="loadingFriends" :rows="3" animated />
      <el-empty v-else-if="friends.length === 0" description="暂无好友" :image-size="60" />
      <ul v-else class="friend-list">
        <li v-for="f in friends" :key="f.id" class="friend-item" @click="startChatWith(f)">
          <div class="friend-avatar">{{ (f.nickname || f.username).slice(0, 1) }}</div>
          <div>
            <div class="friend-name">{{ f.nickname || f.username }}</div>
            <div class="friend-username">@{{ f.username }}</div>
          </div>
        </li>
      </ul>
    </el-dialog>
  </div>
</template>

<style scoped>
.chat-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.banner {
  background: #fff7e6;
  color: #ad6800;
  font-size: 12px;
  padding: 6px 16px;
  border-bottom: 1px solid #ffe7ba;
}

.banner-error {
  background: #fff1f0;
  color: var(--im-failed);
  border-bottom-color: #ffccc7;
}

.chat-body {
  flex: 1;
  display: flex;
  min-height: 0;
}

.side {
  width: 288px;
  flex: 0 0 288px;
  display: flex;
  flex-direction: column;
  background: #fff;
  border-right: 1px solid var(--im-border);
}

.me {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--im-border);
}

.me-avatar {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  background: var(--im-primary);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
}

.me-info {
  flex: 1;
  min-width: 0;
}

.me-name {
  font-size: 14px;
  font-weight: 600;
}

.me-status {
  font-size: 11px;
  color: var(--im-text-weak);
  display: flex;
  align-items: center;
  gap: 4px;
}

.me-unread {
  color: var(--im-failed);
}

.dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--im-offline);
}

.dot.on {
  background: var(--im-online);
}

.side-footer {
  border-top: 1px solid var(--im-border);
  padding: 6px 12px;
  text-align: center;
}

.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: #f7f9fc;
}

.main-header {
  padding: 12px 20px;
  background: #fff;
  border-bottom: 1px solid var(--im-border);
}

.main-title {
  font-size: 15px;
  font-weight: 600;
}

.main-sub {
  font-size: 11px;
  color: var(--im-text-weak);
  margin-top: 3px;
  display: flex;
  align-items: center;
  gap: 5px;
}

.sep {
  color: var(--im-border);
}

.main-empty {
  flex: 1;
}

.friend-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 320px;
  overflow-y: auto;
}

.friend-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 6px;
  border-radius: 8px;
  cursor: pointer;
}

.friend-item:hover {
  background: #f2f5fa;
}

.friend-avatar {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  background: var(--im-primary-light);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
}

.friend-name {
  font-size: 14px;
}

.friend-username {
  font-size: 11px;
  color: var(--im-text-weak);
}
</style>
