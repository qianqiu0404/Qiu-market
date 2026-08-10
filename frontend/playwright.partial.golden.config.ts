import { defineConfig } from '@playwright/test'

const frontendPort = 4177
const harnessPort = Number(process.env.QIU_PARTIAL_GOLDEN_HARNESS_PORT ?? 19093)
const frontendURL = `http://127.0.0.1:${frontendPort}`
const harnessURL = `http://127.0.0.1:${harnessPort}`
const harnessCommand = process.env.QIU_PARTIAL_GOLDEN_HARNESS_COMMAND?.trim() ||
  `go run ./trading/cmd/partial-golden --bind 127.0.0.1:${harnessPort}`

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.partial.golden.spec.ts',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  workers: 1,
  fullyParallel: false,
  use: {
    baseURL: frontendURL,
    channel: 'chrome',
    viewport: { width: 1440, height: 900 },
  },
  webServer: [
    {
      name: 'partial-golden-harness',
      command: harnessCommand,
      cwd: '..',
      env: {
        ...process.env,
        QIU_PARTIAL_GOLDEN_FRONTEND_ORIGIN: frontendURL,
      },
      url: `${harnessURL}/__partial-golden/ready`,
      reuseExistingServer: false,
      timeout: 60_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
    },
    {
      name: 'partial-golden-vue',
      command: `npm run dev -- --port ${frontendPort}`,
      env: {
        ...process.env,
        VITE_API_PROXY_TARGET: harnessURL,
        VITE_TRADING_EVENT_MODE: 'polling',
      },
      url: frontendURL,
      reuseExistingServer: false,
      timeout: 30_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 3_000 },
    },
  ],
})
