<script setup lang="ts">
import { computed, ref } from 'vue'

import { useChatStore } from '@/stores/chat'

const chat = useChatStore()
const text = ref('')

const canSend = computed(() => text.value.trim() !== '' && chat.activeConvId !== null)
const placeholder = computed(() =>
  chat.isConnected ? '输入消息，Enter 发送，Shift+Enter 换行' : '连接已断开，恢复后可继续发送',
)

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
        <template v-if="!chat.isConnected">⚠ 未连接</template>
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
</style>
