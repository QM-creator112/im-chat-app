<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'

import ConversationList from '@/components/ConversationList.vue'
import MessageList from '@/components/MessageList.vue'
import Composer from '@/components/Composer.vue'
import * as api from '@/api'
import { ConversationType } from '@/api/types'
import type { User } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { useChatStore } from '@/stores/chat'

const auth = useAuthStore()
const chat = useChatStore()
const router = useRouter()

// ---------------- 弹窗状态 ----------------
const showFriendPicker = ref(false) // 选好友开始单聊
const showGroupCreate = ref(false) // 建群
const showMembers = ref(false) // 群成员面板
const showAddMembers = ref(false) // 往群里加人

const friends = ref<User[]>([])
const loadingFriends = ref(false)
const submitting = ref(false)

// 建群表单
const groupTitle = ref('')
const selectedForGroup = ref<number[]>([])
// 加人表单
const selectedForAdd = ref<number[]>([])

const myId = computed(() => auth.user?.id ?? 0)
const active = computed(() => chat.activeConversation)
const isGroup = computed(() => active.value?.type === ConversationType.GROUP)
const isOwner = computed(() => isGroup.value && active.value?.ownerId === myId.value)
const members = computed(() =>
  chat.activeConvId === null ? [] : (chat.membersByConv[chat.activeConvId] ?? []),
)
/** 还没在群里的好友（加人弹窗里只显示这些） */
const addableFriends = computed(() => {
  const inGroup = new Set(members.value.map((m) => m.userId))
  return friends.value.filter((f) => !inGroup.has(f.id))
})

const peerOnline = computed<boolean>(() => {
  const conv = active.value
  if (!conv || conv.type !== ConversationType.GROUP) {
    for (const uid of chat.onlineUsers) {
      if (uid !== myId.value) return true
    }
    return false
  }
  return members.value.some((m) => m.userId !== myId.value && chat.onlineUsers.has(m.userId))
})

const reconnectingText = computed(() => {
  if (chat.wsStatus !== 'reconnecting') return ''
  const secs = Math.round((chat.wsNextDelayMs ?? 0) / 100) / 10
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

// ---------------- 好友列表 ----------------
async function ensureFriends(): Promise<void> {
  if (friends.value.length > 0) return
  loadingFriends.value = true
  try {
    const page = await api.listFriends()
    friends.value = page.items
  } catch (e) {
    ElMessage.error(`加载好友失败：${(e as Error).message}`)
  } finally {
    loadingFriends.value = false
  }
}

async function openFriendPicker(): Promise<void> {
  showFriendPicker.value = true
  await ensureFriends()
  if (friends.value.length === 0) ElMessage.info('还没有好友，先添加一个')
}

async function startChatWith(friend: User): Promise<void> {
  showFriendPicker.value = false
  try {
    await chat.startDirectConversation(friend.id)
  } catch (e) {
    ElMessage.error(`创建会话失败：${(e as Error).message}`)
  }
}

// ---------------- 建群 ----------------
async function openGroupCreate(): Promise<void> {
  groupTitle.value = ''
  selectedForGroup.value = []
  showGroupCreate.value = true
  await ensureFriends()
}

async function submitCreateGroup(): Promise<void> {
  const title = groupTitle.value.trim()
  if (title === '') {
    ElMessage.warning('请填写群名称')
    return
  }
  if (selectedForGroup.value.length === 0) {
    ElMessage.warning('至少选择一位成员')
    return
  }
  submitting.value = true
  try {
    const convId = await chat.createGroup(title, selectedForGroup.value)
    showGroupCreate.value = false
    ElMessage.success(`群已创建（会话 ${convId}）`)
  } catch (e) {
    ElMessage.error(`建群失败：${(e as Error).message}`)
  } finally {
    submitting.value = false
  }
}

// ---------------- 成员面板 ----------------
function openMembers(): void {
  showMembers.value = true
  if (chat.activeConvId !== null) void chat.loadMembers(chat.activeConvId)
}

async function openAddMembers(): Promise<void> {
  selectedForAdd.value = []
  showAddMembers.value = true
  await ensureFriends()
  if (addableFriends.value.length === 0) ElMessage.info('所有好友都已在群里')
}

async function submitAddMembers(): Promise<void> {
  if (selectedForAdd.value.length === 0) {
    ElMessage.warning('请选择要添加的好友')
    return
  }
  submitting.value = true
  try {
    const added = await chat.addMembersToActive(selectedForAdd.value)
    showAddMembers.value = false
    ElMessage.success(added > 0 ? `已添加 ${added} 人` : '所选用户都已在群里')
  } catch (e) {
    ElMessage.error(`添加成员失败：${(e as Error).message}`)
  } finally {
    submitting.value = false
  }
}

async function handleLeaveGroup(): Promise<void> {
  const conv = active.value
  if (!conv) return
  try {
    await ElMessageBox.confirm(
      `确定退出「${conv.title}」吗？退群后你将不再收到该群的新消息（历史消息仍保留在服务端）。`,
      '退群确认',
      { type: 'warning', confirmButtonText: '退群', cancelButtonText: '取消' },
    )
  } catch {
    return // 用户取消
  }
  try {
    await chat.leaveActiveGroup()
    showMembers.value = false
    ElMessage.success('已退出群聊')
  } catch (e) {
    // 群主退群会被后端拒绝（1001），这里如实展示后端给的提示
    ElMessage.error((e as Error).message)
  }
}

async function handleLogout(): Promise<void> {
  chat.reset()
  await auth.logout()
  await router.replace({ name: 'login' })
}

function memberOnline(uid: number): boolean {
  return chat.onlineUsers.has(uid)
}
</script>

<template>
  <div class="chat-page">
    <div v-if="chat.wsStatus === 'reconnecting'" class="banner">
      ⟳ {{ reconnectingText }}
    </div>
    <div v-else-if="chat.wsStatus === 'closed'" class="banner banner-error">
      连接已关闭，请重新登录
    </div>

    <div class="chat-body">
      <!-- 左侧 -->
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
          <div class="me-actions">
            <el-tooltip content="发起单聊" placement="bottom">
              <el-button link type="primary" @click="openFriendPicker">＋</el-button>
            </el-tooltip>
            <el-tooltip content="创建群聊" placement="bottom">
              <el-button link type="primary" @click="openGroupCreate">群</el-button>
            </el-tooltip>
          </div>
        </div>

        <ConversationList />

        <div class="side-footer">
          <el-button link @click="handleLogout">退出登录</el-button>
        </div>
      </div>

      <!-- 右侧 -->
      <main class="main">
        <header class="main-header">
          <div class="main-header-left">
            <div class="main-title">
              {{ active?.title || '选择一个会话' }}
              <el-tag v-if="isGroup" size="small" type="info" effect="plain" class="group-tag">
                群聊 {{ members.length > 0 ? members.length + ' 人' : '' }}
              </el-tag>
            </div>
            <div v-if="active" class="main-sub">
              <span class="dot" :class="peerOnline ? 'on' : 'off'" />
              {{ peerOnline ? '有人在线' : '都离线' }}
              <span class="sep">·</span>
              最新 seq {{ active.latestSeq }}
              <template v-if="isOwner">
                <span class="sep">·</span>
                <el-tag size="small" effect="plain" type="warning">我是群主</el-tag>
              </template>
            </div>
          </div>
          <el-button v-if="isGroup" link type="primary" @click="openMembers">成员</el-button>
        </header>

        <el-empty
          v-if="!active"
          description="选一个会话开始聊天，或点左上角 ＋ / 群 发起新会话"
          :image-size="100"
          class="main-empty"
        />

        <template v-else>
          <MessageList />
          <Composer />
        </template>
      </main>
    </div>

    <!-- 选好友单聊 -->
    <el-dialog v-model="showFriendPicker" title="选择好友开始单聊" width="400px">
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

    <!-- 建群 -->
    <el-dialog v-model="showGroupCreate" title="创建群聊" width="440px">
      <el-form label-position="top">
        <el-form-item label="群名称">
          <el-input v-model="groupTitle" maxlength="32" placeholder="例如：产品需求群" />
        </el-form-item>
        <el-form-item :label="`选择成员（已选 ${selectedForGroup.length} 人）`">
          <el-skeleton v-if="loadingFriends" :rows="2" animated />
          <el-empty v-else-if="friends.length === 0" description="暂无好友可邀请" :image-size="50" />
          <el-checkbox-group v-else v-model="selectedForGroup" class="member-checks">
            <el-checkbox v-for="f in friends" :key="f.id" :value="f.id" :label="f.id">
              {{ f.nickname || f.username }}
            </el-checkbox>
          </el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showGroupCreate = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitCreateGroup">创建</el-button>
      </template>
    </el-dialog>

    <!-- 群成员面板 -->
    <el-dialog v-model="showMembers" :title="`群成员（${members.length} 人）`" width="420px">
      <ul class="member-list">
        <li v-for="m in members" :key="m.userId" class="member-item">
          <div class="member-avatar">{{ (m.nickname || m.username).slice(0, 1) }}</div>
          <div class="member-info">
            <div class="member-name">
              {{ m.nickname || m.username }}
              <el-tag v-if="m.userId === active?.ownerId" size="small" type="warning" effect="plain">群主</el-tag>
              <el-tag v-if="m.userId === myId" size="small" type="info" effect="plain">我</el-tag>
            </div>
            <div class="member-sub">
              <span class="dot" :class="memberOnline(m.userId) ? 'on' : 'off'" />
              {{ memberOnline(m.userId) ? '在线' : '离线' }}
              <span class="sep">·</span>
              已读到 seq {{ m.lastReadSeq }}
            </div>
          </div>
        </li>
      </ul>
      <template #footer>
        <el-button @click="openAddMembers">＋ 添加成员</el-button>
        <!-- 群主不能退群，所以不显示按钮（后端也会拦） -->
        <el-button v-if="!isOwner" type="danger" plain @click="handleLeaveGroup">退出群聊</el-button>
        <el-button @click="showMembers = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- 加人入群 -->
    <el-dialog v-model="showAddMembers" title="添加成员" width="420px">
      <el-skeleton v-if="loadingFriends" :rows="2" animated />
      <el-empty v-else-if="addableFriends.length === 0" description="所有好友都已在群里" :image-size="50" />
      <el-checkbox-group v-else v-model="selectedForAdd" class="member-checks">
        <el-checkbox v-for="f in addableFriends" :key="f.id" :value="f.id" :label="f.id">
          {{ f.nickname || f.username }}
        </el-checkbox>
      </el-checkbox-group>
      <template #footer>
        <el-button @click="showAddMembers = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitAddMembers">添加</el-button>
      </template>
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

.me-actions {
  display: flex;
  gap: 2px;
  flex: 0 0 auto;
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
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.main-title {
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 8px;
}

.group-tag {
  font-size: 11px;
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

.friend-list,
.member-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 340px;
  overflow-y: auto;
}

.friend-item,
.member-item {
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

.friend-avatar,
.member-avatar {
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  border-radius: 50%;
  background: var(--im-primary-light);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
}

.friend-name,
.member-name {
  font-size: 14px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.friend-username,
.member-sub {
  font-size: 11px;
  color: var(--im-text-weak);
  display: flex;
  align-items: center;
  gap: 4px;
}

.member-checks {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 260px;
  overflow-y: auto;
  width: 100%;
}
</style>
