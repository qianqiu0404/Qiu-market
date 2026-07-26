import { defineConfig } from '@playwright/test'

const port = Number(process.env.S78_E2E_PORT ?? 4175)
const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  use: {
    baseURL,
    channel: 'chrome',
    viewport: { width: 1440, height: 900 },
  },
  webServer: {
    command: `npm run dev -- --port ${port}`,
    url: baseURL,
    reuseExistingServer: true,
    timeout: 30_000,
  },
})
