<script setup lang="ts">
import type { Conversation } from '@/api/types'
import { useChatStore } from '@/stores/chat'

const chat = useChatStore()

/** 会话列表右上角的时间：今天显示时分，否则显示月/日 */
function formatTime(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) {
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  }
  return `${d.getMonth() + 1}/${d.getDate()}`
}

function avatarText(c: Conversation): string {
  return (c.title || '?').slice(0, 1)
}

function select(id: number): void {
  void chat.selectConversation(id)
}
</script>

<template>
  <aside class="conv-panel">
    <div class="conv-header">
      <el-input placeholder="搜索（暂未实现）" size="small" disabled />
    </div>

    <div v-if="chat.loadingConversations" class="conv-loading">
      <el-skeleton :rows="4" animated />
    </div>

    <el-empty
      v-else-if="chat.conversations.length === 0"
      description="还没有会话，去加个好友开始聊天"
      :image-size="80"
    />

    <ul v-else class="conv-list">
      <li
        v-for="c in chat.conversations"
        :key="c.id"
        class="conv-item"
        :class="{ active: c.id === chat.activeConvId }"
        @click="select(c.id)"
      >
        <div class="avatar">{{ avatarText(c) }}</div>
        <div class="conv-body">
          <div class="conv-row">
            <span class="conv-title">{{ c.title || '未命名会话' }}</span>
            <span class="conv-time">{{ formatTime(c.lastMsgAt) }}</span>
          </div>
          <div class="conv-row">
            <span class="conv-last">{{ c.lastMsg || '还没有消息' }}</span>
            <span v-if="c.unreadCount > 0" class="conv-unread">{{ c.unreadCount }}</span>
          </div>
        </div>
      </li>
    </ul>
  </aside>
</template>

<style scoped>
.conv-panel {
  width: 288px;
  flex: 0 0 288px;
  background: var(--im-bg-sidebar);
  border-right: 1px solid var(--im-border);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.conv-header {
  padding: 12px;
  border-bottom: 1px solid var(--im-border);
}

.conv-loading {
  padding: 16px;
}

.conv-list {
  list-style: none;
  margin: 0;
  padding: 6px 0;
  overflow-y: auto;
  flex: 1;
}

.conv-item {
  display: flex;
  gap: 10px;
  padding: 10px 14px;
  cursor: pointer;
  transition: background 0.15s;
}

.conv-item:hover {
  background: #f2f5fa;
}

.conv-item.active {
  background: #e6f0fa;
}

.avatar {
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  border-radius: 8px;
  background: var(--im-primary-light);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  font-weight: 600;
}

.conv-body {
  flex: 1;
  min-width: 0;
}

.conv-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.conv-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--im-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conv-time {
  font-size: 11px;
  color: var(--im-text-weak);
  flex: 0 0 auto;
}

.conv-last {
  font-size: 12px;
  color: var(--im-text-weak);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.conv-unread {
  flex: 0 0 auto;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--im-failed);
  color: #fff;
  font-size: 11px;
  line-height: 18px;
  text-align: center;
}
</style>
