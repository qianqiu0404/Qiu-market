import { defineConfig } from '@playwright/test'

const frontendPort = 4179
const apiURL = 'http://127.0.0.1:19097'
const frontendURL = `http://127.0.0.1:${frontendPort}`
const harnessCommand = process.env.QIU_QUALITY_GOLDEN_COMMAND?.trim() ||
  'go run ./cmd/quality-golden'

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/quality.golden.spec.ts',
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
      name: 'quality-golden-harness', command: harnessCommand, cwd: '..',
      url: `${apiURL}/healthz`, reuseExistingServer: false, timeout: 60_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
    },
    {
      name: 'quality-golden-vue', command: `npm run build && npm run preview -- --port ${frontendPort}`,
      env: { ...process.env, VITE_API_PROXY_TARGET: apiURL },
      url: frontendURL, reuseExistingServer: false, timeout: 30_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 3_000 },
    },
  ],
})
