import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 单二进制部署：产物由 `make web` 拷贝到 internal/web/dist，并由 go:embed
// 打进二进制。资源使用根路径，保证任意 SPA fallback 页面都能加载 assets。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // 开发环境把 API 代理到本地后端，生产环境同源无需代理。
    proxy: {
      '/v1': 'http://127.0.0.1:8720',
      '/healthz': 'http://127.0.0.1:8720',
      '/readyz': 'http://127.0.0.1:8720',
      '/version': 'http://127.0.0.1:8720',
    },
  },
})
