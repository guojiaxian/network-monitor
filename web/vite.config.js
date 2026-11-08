import { readFileSync } from 'node:fs'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 版本号唯一来源：web/package.json（发版时与 Makefile VERSION 同步）
const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8'))

export default defineConfig({
  plugins: [vue()],
  base: './',
  define: { __APP_VERSION__: JSON.stringify(pkg.version) },
  server: {
    proxy: { '/api': 'http://127.0.0.1:28765' }
  }
})
