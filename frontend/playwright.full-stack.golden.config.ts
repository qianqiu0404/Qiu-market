import { defineConfig } from '@playwright/test'
import path from 'node:path'

function loopbackOrigin(name: string): string {
  const raw = process.env[name]?.trim()
  if (!raw) throw new Error(`${name} is required; run the one-command full-stack gate`)
  const value = new URL(raw)
  if (value.protocol !== 'http:' || value.hostname !== '127.0.0.1' || value.username || value.password || value.pathname !== '/' || value.search || value.hash) {
    throw new Error(`${name} must be an exact credential-free http://127.0.0.1:<port> origin`)
  }
  return value.origin
}

const frontendOrigin = loopbackOrigin('QIU_FULLSTACK_FRONTEND_ORIGIN')
loopbackOrigin('QIU_FULLSTACK_API_ORIGIN')
const manifest = process.env.QIU_FULLSTACK_MANIFEST?.trim()
if (!manifest || !path.isAbsolute(manifest)) {
  throw new Error('QIU_FULLSTACK_MANIFEST must be an absolute path created by the full-stack gate')
}

export default defineConfig({
  testDir: './e2e',
  testMatch: ['**/full-stack.golden.spec.ts', '**/fullstack.static.golden.spec.ts'],
  timeout: 90_000,
  expect: { timeout: 5_000 },
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  use: {
    baseURL: frontendOrigin,
    channel: 'chrome',
    viewport: { width: 1440, height: 1000 },
    reducedMotion: 'reduce',
    contextOptions: { reducedMotion: 'reduce' },
    actionTimeout: 5_000,
    navigationTimeout: 5_000,
  },
})
