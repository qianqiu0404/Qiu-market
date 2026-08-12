import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const releaseCommitCandidate = (
  process.env.QIU_MARKET_RELEASE_COMMIT ?? process.env.VERCEL_GIT_COMMIT_SHA ?? ''
).trim().toLowerCase()
const releaseCommit = /^[0-9a-f]{40}$/.test(releaseCommitCandidate)
  ? releaseCommitCandidate
  : ''

// https://vite.dev/config/
const marketAPIProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:9092'
const tradingAPIProxyTarget = process.env.VITE_TRADING_API_PROXY_TARGET ?? marketAPIProxyTarget
const apiProxy = {
  '/api/v1/trading': {
    target: tradingAPIProxyTarget,
    changeOrigin: true,
    ws: true,
  },
  '/api': {
    target: marketAPIProxyTarget,
    changeOrigin: true,
    ws: true,
  },
}

export default defineConfig({
  plugins: [vue()],
  define: {
    __QIU_MARKET_RELEASE_COMMIT__: JSON.stringify(releaseCommit),
  },
  build: {
    chunkSizeWarningLimit: 1300,
  },
  server: {
    host: '127.0.0.1',
    port: 5174,
    strictPort: true,
    proxy: apiProxy,
  },
  preview: {
    host: '127.0.0.1',
    strictPort: true,
    proxy: apiProxy,
  },
})
