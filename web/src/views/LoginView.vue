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

      <!--
        这里只说**用户能感知的收益**，不讲实现。
        「access token 30 分钟 / refresh 14 天 / 前端自动刷新」这类内容属于
        接口契约与面试素材，已写到 docs/api.md 与 docs/08-面试问答.md：
          · 登录页的任务是让人进来，不是介绍架构
          · 公开 token 有效期与刷新机制，等于给攻击者省侦察成本
      -->
      <p class="tip">
        登录状态保留 14 天，期间不用重复登录。
      </p>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  min-height: 100dvh; /* 手机地址栏伸缩时 100vh 会露白底 */
  display: flex;
  align-items: center;
  justify-content: center;
  /* framed-sheet: 地是平的冷灰，渐变属于被禁的装饰 */
  background: var(--im-ground);
  padding: 24px;
}

/* 卡白纸：1px 边 + 无阴影（边框与阴影二选一；静止的面不该浮起） */
.login-card {
  width: 100%;
  max-width: 400px;
  background: var(--im-surface);
  border: 1px solid var(--im-border);
  border-radius: var(--im-radius-sheet);
  padding: 36px 32px 24px;
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

/* 说明文字：抬到 13px（原来 12px 拉低了整页正文比例）
   虚线分隔属被禁的装饰线 —— 只留间距 */
.tip {
  margin: 24px 0 0;
  font-size: var(--im-fs-small);
  line-height: 1.65;
  color: var(--im-muted);
}

/* ============================================================================
 * 响应式：手机（< 768px）
 * 注意位置：必须放在样式块**末尾**。
 * 放前面的话，后面的基础规则（.logo 52px 等）同权重会把它覆盖掉。
 * ========================================================================== */
@media (max-width: 767px) {
  .login-page {
    align-items: flex-start;
    padding: 16px;
    padding-top: calc(16px + env(safe-area-inset-top, 0px));
    padding-bottom: calc(16px + env(safe-area-inset-bottom, 0px));
  }

  .login-card {
    margin-top: 6vh;
    padding: 26px 20px 20px;
    border-radius: 12px;
  }

  .logo {
    width: 44px;
    height: 44px;
    line-height: 44px;
    font-size: 20px;
  }

  /* iOS 聚焦输入框时若字号 < 16px 会自动放大整页 */
  .login-card :deep(.el-input__inner) {
    font-size: 16px;
  }

  .login-card :deep(.el-button) {
    min-height: 44px;
  }
}
</style>