/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// Vite 配置。
//
// 关键点：开发时把 /api 与 /ws 代理到后端（127.0.0.1:8080）。
// 这样做的好处是**前端代码里只写相对路径**，开发和生产的调用方式完全一致，
// 而且不存在跨域问题（同源），不需要后端配 CORS。
//
// 生产环境由 Nginx 做同样的事：/api 与 /ws 反代到 Go 服务，
// 其余路径返回 dist 里的静态文件。
export default defineConfig({
  plugins: [vue()],

  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },

  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      // WebSocket 必须显式开 ws: true，否则握手不会被代理
      '/ws': {
        target: 'ws://127.0.0.1:8080',
        ws: true,
      },
    },
  },

  build: {
    outDir: 'dist',
    // 关掉 sourcemap 之外的花哨功能，保持产物简单可读
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },

  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    globals: true,
  },
})
