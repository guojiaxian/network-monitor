import { createApp } from 'vue'
import 'bootstrap/dist/css/bootstrap.min.css'
import './style.css'
import App from './App.vue'
import { router } from './router'

// 大屏公开地址兼容：/screen、/screen/floor（不带 # 的直链/收藏）跳到 hash 路由。
// 只看 pathname：vue-router 的 createWebHashHistory 在模块导入时就会把空 hash 规范成 "#/"，
// 此时再判断 location.hash 已经来不及（会被当首页 → 401 → #/login）。
// 跳转分支不能再 mount，否则首页会抢在导航前请求受保护接口并把地址改回 #/login。
const path = location.pathname.replace(/\/+$/, '')
if (path === '/screen' || path === '/screen/floor') {
  location.replace('/#' + path)
} else {
  createApp(App).use(router).mount('#app')
}
