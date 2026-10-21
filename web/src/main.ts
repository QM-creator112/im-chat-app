import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import App from './App.vue'
import router from './router'
import './styles/main.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
// 全量引入 Element Plus：对个人项目来说省事、类型完整，
// 代价是产物偏大。若要做体积优化，后续可换成按需引入。
app.use(ElementPlus, { locale: zhCn })

app.mount('#app')
