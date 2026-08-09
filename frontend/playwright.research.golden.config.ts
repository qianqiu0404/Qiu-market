import { defineConfig } from '@playwright/test'

const frontendPort = 4178
const apiURL = 'http://127.0.0.1:19096'
const frontendURL = `http://127.0.0.1:${frontendPort}`
const harnessCommand = process.env.QIU_RESEARCH_GOLDEN_COMMAND?.trim() ||
  'go run ./cmd/research-golden'

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/research.golden.spec.ts',
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
      name: 'research-golden-harness',
      command: harnessCommand,
      cwd: '..',
      url: `${apiURL}/healthz`,
      reuseExistingServer: false,
      timeout: 60_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
    },
    {
      name: 'research-golden-vue',
      command: `npm run build && npm run preview -- --port ${frontendPort}`,
      env: {
        ...process.env,
        VITE_API_PROXY_TARGET: apiURL,
      },
      url: frontendURL,
      reuseExistingServer: false,
      timeout: 30_000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 3_000 },
    },
  ],
})
