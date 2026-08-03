import { defineConfig } from '@playwright/test'

const port = Number(process.env.S78_E2E_PORT ?? 4175)
const baseURL = `http://127.0.0.1:${port}`
const reuseExistingServer = process.env.S78_E2E_REUSE_SERVER === '1'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  // All suites exercise the same stateful Qiu Market SPA. Serial workers keep
  // the shared Vite/browser lifecycle deterministic during the combined gate.
  workers: 1,
  use: {
    baseURL,
    channel: 'chrome',
    viewport: { width: 1440, height: 900 },
  },
  webServer: {
    command: `npm run dev -- --port ${port}`,
    url: baseURL,
    reuseExistingServer,
    timeout: 30_000,
  },
})
