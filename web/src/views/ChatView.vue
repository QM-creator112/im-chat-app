<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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

// ---------------- 移动端视图切换（master-detail）----------------
/**
 * 手机上「会话列表」与「聊天区」是两屏，这里只控制显示哪一屏。
 *
 * **刻意不去改 activeConvId**：点返回不会退订、不会清空消息、不会重新拉历史，
 * 从列表点回同一个会话是瞬时的。桌面端这个值完全不起作用（靠媒体查询隔离）。
 */
const mobileListOpen = ref(true)

watch(
  () => active.value?.id,
  (id) => {
    // 选中会话 → 自动切到聊天屏；会话被取消（如退群）→ 回到列表
    mobileListOpen.value = id == null
  },
)
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

/**
 * 触发 AI 总结。
 *
 * 后端的设计是「AI 失败也返回 200，失败降级成一条系统消息」，
 * 所以这里通常不会进 catch —— 只有"不是成员""请求参数错"这类真故障才会。
 */
async function handleAskAI(): Promise<void> {
  if (chat.activeConvId === null) return
  try {
    await chat.askAI('summary')
  } catch (e) {
    ElMessage.error(`AI 请求失败：${(e as Error).message}`)
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
  <!--
    响应式说明：mobile-list 这个类只在 <768px 生效，用来切换手机上显示哪一屏。
    桌面端侧栏（288px）与聊天区并排，这个类不起任何作用。
  -->
  <div class="chat-page" :class="{ 'mobile-list': mobileListOpen }">
    <div v-if="chat.wsStatus === 'reconnecting'" class="banner">
      ⟳ {{ reconnectingText }}
    </div>
    <div v-else-if="chat.wsStatus === 'closed'" class="banner banner-error">
      连接已关闭，请重新登录
    </div>

    <div class="chat-body">
      <!-- 左侧：框（导航 + 列表），白纸之外的地 -->
      <div class="side">
        <!-- 产品标识（house rule：截图里要能读出这是什么产品） -->
        <div class="brand">
          <span class="brand-mark" aria-hidden="true">IM</span>
          <span class="brand-name">IM 即时通信</span>
        </div>
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
          <!-- 手机上才有：返回会话列表（桌面由 CSS 隐藏） -->
          <button
            v-if="active"
            class="mobile-back"
            type="button"
            aria-label="返回会话列表"
            @click="mobileListOpen = true"
          >
            ←
          </button>
          <div class="main-header-left">
            <h1 class="main-title">
              <span class="main-title-text">{{ active?.title || '选择一个会话' }}</span>
              <el-tag v-if="isGroup" size="small" type="info" effect="plain" class="group-tag">
                群聊 {{ members.length > 0 ? members.length + ' 人' : '' }}
              </el-tag>
            </h1>
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
          <div class="header-actions">
            <el-button
              v-if="active"
              link
              type="primary"
              :loading="chat.aiLoading"
              @click="handleAskAI"
            >
              AI 总结
            </el-button>
            <el-button v-if="isGroup" link type="primary" @click="openMembers">成员</el-button>
          </div>
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
  /* framed-sheet：整体地 = 框的色，白纸浮在它上面 */
  background: var(--im-ground);
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

/* 框里的侧栏：与地同色，不放分隔线（framed-sheet 的 off-style 明确禁止侧栏与顶栏之间加线） */
.side {
  width: var(--im-sidebar);
  flex: 0 0 var(--im-sidebar);
  display: flex;
  flex-direction: column;
  background: transparent;
  min-height: 0;
}

/* 产品标识：品牌色只出现在这个 24px 方块上 */
.brand {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 14px 16px 10px;
}

.brand-mark {
  width: 24px;
  height: 24px;
  border-radius: var(--im-radius-control);
  background: var(--im-primary);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.02em;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
}

.brand-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--im-text);
  letter-spacing: -0.01em;
}

/* 账号行：作为「切换器」放在侧栏顶部，与产品名同为框的一部分 */
.me {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px 12px;
  /* 与列表之间靠色阶与间距分隔，不用线 */
  margin-bottom: 4px;
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
  font-size: var(--im-fs-meta);
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

/* 白纸：右/底内缩 8px、16px 圆角、1px 边、无阴影 —— 层次只靠一个色阶 */
.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: var(--im-surface);
  margin: 0 var(--im-sheet-inset) var(--im-sheet-inset) 0;
  border: 1px solid var(--im-border);
  border-radius: var(--im-radius-sheet);
  overflow: hidden;
}

/* 纸内的顶栏 48px：标题 + 一个动作。与消息区之间用 1px 行线（行线是 app screen 允许的） */
.main-header {
  padding: 0 20px;
  min-height: 52px;
  background: var(--im-surface);
  border-bottom: 1px solid var(--im-rule);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: 0 0 auto;
}

.main-title {
  font-size: var(--im-fs-title);
  font-weight: 600;
  letter-spacing: -0.01em;
  margin: 0; /* h1 默认有外边距，会把 48px 顶栏撑开 */
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0; /* 让长标题能收缩，否则会把群标签挤出屏幕 */
}

.main-title-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-tag {
  font-size: var(--im-fs-meta);
  flex: 0 0 auto;
}

.main-sub {
  font-size: var(--im-fs-meta);
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
  background: var(--im-raised);
}

.friend-avatar,
.member-avatar {
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  border-radius: 50%;
  background: var(--im-active);
  color: var(--im-primary);
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
  font-size: var(--im-fs-meta);
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
/* ============================================================================
 * 返回按钮：桌面端不显示，只有手机断点里才出现
 * ========================================================================== */
.mobile-back {
  display: none;
}

/* ============================================================================
 * 响应式：手机（< 768px）
 * ----------------------------------------------------------------------------
 * 为什么必须改：桌面是「侧栏 288px + 聊天区自适应」并排。
 * 在 375px 宽的手机上，聊天区只剩 375 - 288 = 87px，
 * 实测标题会竖排成一列、输入框被压成一条缝（见 build/shots-before/）。
 *
 * 所以手机上改成 master-detail 两屏：
 *   · 默认：会话列表铺满全屏
 *   · 选中会话后：聊天区铺满全屏，左上角出现返回按钮
 * 两屏之间不重新拉数据，只是 CSS 切换显示。
 * ========================================================================== */
@media (max-width: 767px) {
  /* 手机浏览器地址栏会伸缩，100vh 会露白底；100dvh 跟着可视区走。
     两行都写：不认 dvh 的浏览器用第一行，认的用第二行覆盖。 */
  .chat-page {
    height: 100vh;
    height: 100dvh;
  }

  .banner {
    font-size: 12px;
    padding: 6px 12px;
  }

  .chat-body {
    position: relative;
  }

  /* ---- 列表屏 ---- */
  .side {
    width: 100%;
    flex: 1 1 auto;
    border-right: none;
  }

  /* 手机上没有「聊天空屏」这一屏：没选会话时就应该看列表 */
  .main {
    display: none;
  }

  /* ---- 聊天屏：隐藏列表，聊天区铺满 ---- */
  .chat-page:not(.mobile-list) .side {
    display: none;
  }

  .chat-page:not(.mobile-list) .main {
    display: flex;
    width: 100%;
    flex: 1 1 auto;
  }

  /* 44px 触屏命中区（iOS/Android 建议下限） */
  .mobile-back {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    margin: -8px 2px -8px -8px;
    padding: 0;
    border: none;
    border-radius: 8px;
    background: transparent;
    color: var(--im-text, #1f2329);
    font-size: 20px;
    line-height: 1;
    cursor: pointer;
    flex: 0 0 auto;
  }

  .mobile-back:active {
    background: #eef2f7;
  }

  /* 头部紧凑：把宽度让给标题 */
  .main-header {
    padding: 8px 12px;
    gap: 8px;
  }

  .main-header-left {
    min-width: 0;
    flex: 1 1 auto;
  }

  .main-title {
    font-size: 15px;
    gap: 6px;
  }

  .main-sub {
    font-size: var(--im-fs-meta);
    flex-wrap: wrap;
    row-gap: 2px;
  }

  /* 头部按钮改用触屏合适的尺寸，别把标题挤没 */
  .header-actions :deep(.el-button) {
    min-height: 36px;
    padding: 6px 8px;
    font-size: 13px;
  }

  /* 手机上的弹窗（好友/建群/成员）改成接近全宽、可滚动 */
  :deep(.el-dialog) {
    width: calc(100vw - 24px) !important;
    max-width: calc(100vw - 24px);
    margin-top: 6vh !important;
  }

  .friend-list,
  .member-list {
    max-height: 52vh;
  }

  /* 触屏命中区：列表项至少 56px 高 */
  .friend-item,
  .member-item {
    padding: 12px 8px;
  }
}
</style>
