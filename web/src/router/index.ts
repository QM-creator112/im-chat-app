import { createRouter, createWebHistory } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { public: true },
    },
      {
        // 产品首页（落地页）：公开可访问，让面试官先看到门面
        path: '/welcome',
        name: 'welcome',
        component: () => import('@/views/WelcomeView.vue'),
        meta: { public: true },
      },
    {
      path: '/',
      name: 'chat',
      component: () => import('@/views/ChatView.vue'),
    },
    // 兜底：未知路径回首页（未登录会被守卫拦到登录页）
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// 全局前置守卫：没有 token 就跳登录页。
//
// 注意这里只判断「有没有 token」，不判断「token 是否有效」——
// 有效性由后端说了算（401 时由 api client 统一处理刷新/登出）。
// 在路由守卫里做网络校验会让每次跳转都变慢，而且离线时会把用户挡在门外。
router.beforeEach((to) => {
  const auth = useAuthStore()
  if (!to.meta.public && !auth.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && auth.isLoggedIn) {
    return { name: 'chat' }
  }
  return true
})

export default router
