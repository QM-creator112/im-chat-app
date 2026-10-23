<script setup lang="ts">
import { computed, ref } from 'vue'

import { useChatStore } from '@/stores/chat'

const chat = useChatStore()
const text = ref('')

const canSend = computed(() => text.value.trim() !== '' && chat.activeConvId !== null)

/**
 * 触屏设备上没有物理键盘，「Enter 发送 / Shift+Enter 换行」这句提示是噪音，
 * 而且 iOS 的软键盘回车键行为也不一样。用 `(hover: none)` 判断一次即可
 * （桌面鼠标设备 hover 可用，触屏为 none）。
 */
const isTouch =
  typeof window !== 'undefined' && window.matchMedia?.('(hover: none)').matches === true

const placeholder = computed(() => {
  if (!chat.isConnected) return '连接已断开，恢复后可继续发送'
  return isTouch ? '输入消息…' : '输入消息，Enter 发送，Shift+Enter 换行'
})

function submit(): void {
  if (!canSend.value) return
  chat.sendMessage(text.value)
  text.value = ''
}

/** Enter 发送 / Shift+Enter 换行 */
function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    submit()
  }
}
</script>

<template>
  <div class="composer">
    <el-input
      v-model="text"
      type="textarea"
      :rows="2"
      resize="none"
      :placeholder="placeholder"
      maxlength="8000"
      @keydown="onKeydown"
    />
    <div class="composer-bar">
      <span class="hint">
        <template v-if="!chat.isConnected">未连接</template>
        <template v-else-if="chat.wsStatus === 'reconnecting'">
          正在重连（第 {{ chat.wsAttempt }} 次）
        </template>
        <template v-else>● 已连接</template>
      </span>
      <el-button type="primary" :disabled="!canSend" @click="submit">发送</el-button>
    </div>
  </div>
</template>

<style scoped>
.composer {
  border-top: 1px solid var(--im-border);
  background: #fff;
  padding: 10px 14px 8px;
}

.composer-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 8px;
}

.hint {
  font-size: 12px;
  color: var(--im-text-weak);
}
/* ============================================================================
 * 响应式：手机（< 768px）
 *
 * 注意：.hint 显示的是 **WebSocket 连接状态**（已连接 / 正在重连 / 未连接），
 * 不是键盘提示 —— 手机上同样必要，所以这里**不能隐藏**。
 * ========================================================================== */
@media (max-width: 767px) {
  .composer {
    padding: 8px 10px calc(8px + env(safe-area-inset-bottom, 0px));
  }

  .composer-bar {
    margin-top: 6px;
    /* 状态文字可能变长（"正在重连（第 N 次）"），别把按钮挤出去 */
    gap: 8px;
  }

  .hint {
    font-size: 12px;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .composer-bar :deep(.el-button) {
    min-height: 40px;
    padding: 0 20px;
    font-size: 15px;
    flex: 0 0 auto;
  }

  /* 输入框字号 ≥16px，避免 iOS 聚焦时自动放大整页 */
  .composer :deep(.el-textarea__inner) {
    font-size: 16px;
  }
}
</style>
