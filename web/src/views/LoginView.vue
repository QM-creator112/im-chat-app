<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const mode = ref<'login' | 'register'>('login')
const form = reactive({ username: '', password: '', nickname: '' })
const submitting = ref(false)
const localError = ref('')

const isRegister = computed(() => mode.value === 'register')
const title = computed(() => (isRegister.value ? '注册新账号' : '登录'))

function switchMode(): void {
  mode.value = isRegister.value ? 'login' : 'register'
  localError.value = ''
  auth.error = ''
}

async function submit(): Promise<void> {
  localError.value = ''
  if (form.username.trim() === '' || form.password === '') {
    localError.value = '用户名和密码都不能为空'
    return
  }
  submitting.value = true
  try {
    if (isRegister.value) {
      await auth.register(form.username.trim(), form.password, form.nickname.trim() || undefined)
    } else {
      await auth.login(form.username.trim(), form.password)
    }
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    await router.replace(redirect)
  } catch (e) {
    localError.value = (e as Error).message
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="brand">
        <div class="logo">聊</div>
        <h1>IM 即时通信</h1>
        <p class="subtitle">{{ title }}</p>
      </div>

      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名">
          <el-input
            v-model="form.username"
            placeholder="4-32 位，字母开头"
            autocomplete="username"
            @keyup.enter="submit"
          />
        </el-form-item>

        <el-form-item label="密码">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            placeholder="8-64 位，需含字母和数字"
            autocomplete="current-password"
            @keyup.enter="submit"
          />
        </el-form-item>

        <el-form-item v-if="isRegister" label="昵称（可选）">
          <el-input v-model="form.nickname" placeholder="不填则用用户名" @keyup.enter="submit" />
        </el-form-item>

        <el-alert
          v-if="localError || auth.error"
          :title="localError || auth.error"
          type="error"
          :closable="false"
          show-icon
          class="login-error"
        />

        <el-button
          type="primary"
          size="large"
          class="submit-btn"
          :loading="submitting"
          @click="submit"
        >
          {{ title }}
        </el-button>

        <div class="switch">
          {{ isRegister ? '已经有账号？' : '还没有账号？' }}
          <el-link type="primary" :underline="false" @click="switchMode">
            {{ isRegister ? '去登录' : '去注册' }}
          </el-link>
        </div>
      </el-form>

      <p class="tip">
        登录后拿到 access token（30 分钟）与 refresh token（14 天），
        token 过期时前端自动刷新，用户无感。
      </p>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(140deg, #eef2f9 0%, #f7f9fc 60%, #eaf1fb 100%);
  padding: 24px;
}

.login-card {
  width: 100%;
  max-width: 400px;
  background: #fff;
  border-radius: 14px;
  padding: 36px 32px 24px;
  box-shadow: 0 12px 40px rgba(31, 56, 100, 0.12);
}

.brand {
  text-align: center;
  margin-bottom: 22px;
}

.logo {
  width: 52px;
  height: 52px;
  line-height: 52px;
  margin: 0 auto 12px;
  border-radius: 50%;
  background: var(--im-primary);
  color: #fff;
  font-size: 24px;
  font-weight: 600;
}

h1 {
  margin: 0;
  font-size: 21px;
  color: var(--im-primary);
  letter-spacing: 1px;
}

.subtitle {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--im-text-weak);
}

.login-error {
  margin-bottom: 14px;
}

.submit-btn {
  width: 100%;
  background: var(--im-primary);
  border-color: var(--im-primary);
}

.switch {
  margin-top: 14px;
  text-align: center;
  font-size: 13px;
  color: var(--im-text-weak);
}

.tip {
  margin: 20px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--im-text-weak);
  border-top: 1px dashed var(--im-border);
  padding-top: 12px;
}
</style>
