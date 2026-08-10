import { defineConfig } from '@playwright/test'

function frontendPortFromEnvironment(raw: string | undefined): number {
  if (raw === undefined) return 4176
  if (!/^[1-9]\d{0,4}$/.test(raw)) {
    throw new Error('QIU_GOLDEN_FRONTEND_PORT must be an integer in [1, 65535]')
  }
  const port = Number(raw)
  if (!Number.isSafeInteger(port) || port > 65_535) {
    throw new Error('QIU_GOLDEN_FRONTEND_PORT must be an integer in [1, 65535]')
  }
  return port
}

const frontendPort = frontendPortFromEnvironment(process.env.QIU_GOLDEN_FRONTEND_PORT)
const harnessPort = Number(process.env.QIU_GOLDEN_HARNESS_PORT ?? 19092)
const frontendURL = `http://127.0.0.1:${frontendPort}`
const harnessURL = `http://127.0.0.1:${harnessPort}`
const harnessCommand = process.env.QIU_GOLDEN_HARNESS_COMMAND?.trim() ||
  `go run ./trading/cmd/golden --bind 127.0.0.1:${harnessPort}`

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/trading.golden.spec.ts',
  timeout: 45_000,
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
      name: 'golden-harness',
      command: harnessCommand,
      cwd: '..',
      env: {
        ...process.env,
        QIU_GOLDEN_HARNESS_ADDR: `127.0.0.1:${harnessPort}`,
        QIU_GOLDEN_FRONTEND_ORIGIN: frontendURL,
      },
      url: `${harnessURL}/__golden/ready`,
      reuseExistingServer: false,
      timeout: 60_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
    },
    {
      name: 'vue',
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
