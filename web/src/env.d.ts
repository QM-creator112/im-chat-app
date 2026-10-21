/// <reference types="vite/client" />

// .vue 文件的类型声明。
//
// Vue 3 + TypeScript 需要它，否则 import Xxx.vue 会被 TS 当成未定义模块报错。
declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}
