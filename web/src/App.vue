<script setup lang="ts">
import { onMounted } from 'vue'

import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

// 页面刷新后把 localStorage 里的会话读回内存。
// 必须在这里做（而不是在 store 定义时做），才能保证 Pinia 已就绪。
onMounted(() => {
  auth.hydrate()
})
</script>

<template>
  <router-view />
</template>
