import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const webPort = Number(process.env.S78_TRADING_WEB_PORT ?? '5175')
const httpTarget = process.env.S78_TRADING_HTTP_TARGET ?? 'http://127.0.0.1:8084'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: '127.0.0.1',
    port: webPort,
    strictPort: true,
    proxy: {
      '/api': {
        target: httpTarget,
        changeOrigin: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
})
