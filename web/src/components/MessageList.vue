<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

import { ConversationType } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { useChatStore } from '@/stores/chat'
import type { ChatMessage } from '@/utils/chatMessage'
import { animateRowArrive } from '@/utils/motion'

const auth = useAuthStore()
const chat = useChatStore()

const scrollRef = ref<HTMLElement | null>(null)

/**
 * 系统消息的 contentType。
 *
 * 后端把 AI 回复也用这个类型落库（而不是伪造一个"用户"），
 * 前端据此渲染成居中卡片 —— AI 说的话不是任何一方说的，不该挂在左右某一侧。
 */
const SYSTEM_CONTENT_TYPE = 5

const messages = computed<ChatMessage[]>(() => chat.activeMessages)

/**
 * 「一行到达」反馈（app-screens 允许的动效之一）
 *
 * 只在**追加一条**时触发：
 *   · 历史分页会一次插入多条 -> 长度差 > 1，跳过（整段重播会很吵）
 *   · 切换会话会整体替换 -> 由 activeConvId 变化处理，长度差也不是 +1
 * 其余按压/悬停/焦点反馈用 CSS，不走 JS。
 */
watch(
  () => messages.value.length,
  async (n, old) => {
    if (old === undefined || n !== old + 1) return
    await nextTick()
    const rows = scrollRef.value?.querySelectorAll('.msg-row')
    animateRowArrive(rows?.[rows.length - 1] ?? null)
  },
)
const myId = computed<number>(() => auth.user?.id ?? 0)

function isMine(m: ChatMessage): boolean {
  return m.senderId === myId.value
}

function formatTime(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

/** 是否显示时间分隔：与上一条间隔超过 5 分钟 */
function showTime(index: number): boolean {
  if (index === 0) return true
  const cur = messages.value[index]
  const prev = messages.value[index - 1]
  if (!cur || !prev) return false
  return cur.createdAt - prev.createdAt > 5 * 60 * 1000
}

const peerReadSeq = computed<number>(() => {
  const id = chat.activeConvId
  return id === null ? 0 : (chat.peerReadSeq[id] ?? 0)
})

const isGroup = computed(() => chat.activeConversation?.type === ConversationType.GROUP)

/** 我发出的最后一条**已确认**消息的 seq —— 群聊只在这条上显示已读人数 */
const lastMineSeq = computed<number>(() => {
  let seq = 0
  for (const m of messages.value) {
    if (isMine(m) && m.seq > seq) seq = m.seq
  }
  return seq
})

/** 群聊「已读 N/M」——N/M 只统计**接收者**（排除发送者自己） */
function groupReadText(seq: number, senderId: number): string {
  const convId = chat.activeConvId
  if (convId === null) return ''
  const { read, total } = chat.readCountOf(convId, seq, senderId)
  if (total === 0) return ''
  if (read >= total) return `全部已读`
  return `已读 ${read}/${total}`
}

function isRead(m: ChatMessage): boolean {
  return m.seq > 0 && m.seq <= peerReadSeq.value
}

/**
 * 滚到底部。
 *
 * 只在用户本来就在底部附近时才自动滚 —— 否则用户正在往上翻历史，
 * 来了新消息把他强行拽回底部，体验很差。
 */
function scrollToBottom(smooth = false): void {
  void nextTick(() => {
    const el = scrollRef.value
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
  })
}

function nearBottom(): boolean {
  const el = scrollRef.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 120
}

watch(
  () => messages.value.length,
  (_n, oldLen) => {
    if (oldLen === undefined || oldLen === 0 || nearBottom()) {
      scrollToBottom()
    }
  },
)

watch(
  () => chat.activeConvId,
  () => {
    scrollToBottom()
  },
)

function retry(m: ChatMessage): void {
  chat.retryMessage(m.clientMsgId)
}

defineExpose({ scrollToBottom })
</script>

<template>
  <div ref="scrollRef" class="msg-scroll">
    <!-- 向上翻页 -->
    <div v-if="chat.hasMore[chat.activeConvId ?? 0]" class="load-more">
      <el-button link type="primary" :loading="chat.loadingMessages" @click="chat.loadMoreHistory()">
        加载更早的消息
      </el-button>
    </div>

    <el-empty
      v-if="messages.length === 0 && !chat.loadingMessages"
      description="还没有消息，说点什么吧"
      :image-size="72"
    />

    <div v-for="(m, i) in messages" :key="m.clientMsgId + '-' + m.seq" class="msg-row" :data-seq="m.seq">
      <div v-if="showTime(i)" class="msg-time">{{ formatTime(m.createdAt) }}</div>

      <!-- 系统 / AI 消息：居中卡片，不挂在左右任何一侧 -->
      <div v-if="m.contentType === SYSTEM_CONTENT_TYPE" class="system-row">
        <div class="system-card">{{ m.content }}</div>
      </div>

      <div v-else class="msg-line" :class="isMine(m) ? 'mine' : 'peer'">
        <div class="bubble" :class="isMine(m) ? 'bubble-mine' : 'bubble-peer'">
          <div v-if="!isMine(m)" class="sender">{{ m.senderName || '对方' }}</div>
          <div class="content">{{ m.content }}</div>
        </div>

        <!-- 三态指示（只对我自己发的消息显示） -->
        <div v-if="isMine(m)" class="meta">
          <template v-if="m.status === 'sending'">
            <span class="state sending">发送中…</span>
          </template>
          <template v-else-if="m.status === 'failed'">
            <span class="state failed">
              发送失败
              <el-link type="danger" :underline="false" @click="retry(m)">· 点击重试</el-link>
            </span>
          </template>
          <template v-else>
            <!-- 群聊：显示「已读 N/M」，反映这条消息被多少成员读到 -->
            <span v-if="isGroup && m.seq === lastMineSeq" class="state read">
              {{ groupReadText(m.seq, m.senderId) }}
            </span>
            <!-- 单聊：对方读到就显示「已读」 -->
            <template v-else>
              <span v-if="isRead(m)" class="state read">已读</span>
              <span v-else class="state sent">已发送</span>
            </template>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 消息区比白纸深一档：白气泡靠「色阶」与底分开，不靠描边
   （app-screens §9.4：边框或色阶二选一）
   消息少时沉到底部，否则下半屏会留一大片空白，读作没做完 */
.msg-scroll {
  /* 消息少时沉底：flex 容器 + 内容底部对齐，否则下半屏一大片空白 */
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 20px 8px;
  background: var(--im-ground);
}

.msg-time {
  text-align: center;
  font-size: var(--im-fs-meta);
  color: var(--im-muted);
  margin: 12px 0 8px;
}

.msg-line {
  display: flex;
  margin-bottom: 12px;
}

/* 系统 / AI 消息：居中卡片 + 左侧强调条 */
.system-row {
  display: flex;
  justify-content: center;
  margin-bottom: 12px;
}

.system-card {
  max-width: min(78%, 620px);
  padding: 10px 14px;
  border-radius: 10px;
  background: #f0f4fa;
  border: 1px solid #dbe4f0;
  /* 色条属装饰；改用整体色调 + 1px 边标记 AI 消息 */
  color: var(--im-text);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.msg-line.mine {
  flex-direction: column;
  align-items: flex-end;
}

.msg-line.peer {
  flex-direction: column;
  align-items: flex-start;
}

.bubble {
  max-width: min(70%, 520px);
  padding: 9px 12px;
  border-radius: 10px;
  font-size: 14px;
  line-height: 1.55;
  word-break: break-word;
  white-space: pre-wrap;
}

.bubble-mine {
  background: var(--im-bubble-mine);
  color: #fff;
  border-bottom-right-radius: 3px;
}

.bubble-peer {
  background: var(--im-bubble-peer);
  color: var(--im-text);
  border: 1px solid var(--im-border);
  border-bottom-left-radius: 3px;
}

.sender {
  font-size: var(--im-fs-meta);
  color: var(--im-text-weak);
  margin-bottom: 3px;
}

.content {
  white-space: pre-wrap;
}

.meta {
  margin-top: 3px;
  font-size: var(--im-fs-meta);
}

.state.sending {
  color: var(--im-text-weak);
}

.state.sent {
  color: var(--im-text-weak);
}

.state.read {
  color: var(--im-muted);
  font-weight: 600;
}

.state.failed {
  color: var(--im-failed);
}
/* ============================================================================
 * 响应式：手机（< 768px）
 *  · 减小左右留白，把宽度让给消息本体
 *  · 气泡放宽到 82%（桌面 70%），手机上 70% 会折行过多
 * ========================================================================== */
@media (max-width: 767px) {
  /* 消息区比白纸深一档：白气泡靠「色阶」与底分开，不靠描边
   （app-screens §9.4：边框或色阶二选一）
   消息少时沉到底部，否则下半屏会留一大片空白，读作没做完 */
.msg-scroll {
  justify-content: flex-end;
    padding: 12px 12px 8px;
  }

  .bubble {
    max-width: min(82%, 520px);
  }

  .system-card {
    max-width: 92%;
  }

  .msg-time {
    margin: 8px 0 6px;
  }

  .msg-line {
    margin-bottom: 10px;
  }
}
</style>
