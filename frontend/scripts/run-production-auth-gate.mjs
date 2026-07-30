#!/usr/bin/env node

import { spawnSync } from 'node:child_process'
import { chmod, mkdir, readFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { chromium, request } from '@playwright/test'

import {
  balanceAvailable,
  canonicalJSON,
  invariant,
  loopbackHTTPProxy,
  oauthCallbackError,
  parseDecimalAtoms,
  requestID,
  requirePrivateRegularFile,
  validateReleaseProvenance,
  writePrivateJSON,
} from './live-gate-lib.mjs'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(scriptDirectory, '../..')
const supportDir = process.env.QIU_MARKET_SUPPORT_DIR ??
  path.join(os.homedir(), 'Library/Application Support/Qiu Market')
const releaseStateFile = process.env.QIU_MARKET_VERCEL_RELEASE_STATE ??
  path.join(supportDir, 'vercel-release/active.json')
const evidenceFile = process.env.QIU_MARKET_PRODUCTION_AUTH_EVIDENCE_FILE ??
  path.join(supportDir, 'observations/production-auth-evidence.json')
const browserProfile = process.env.QIU_MARKET_GATE_BROWSER_PROFILE ??
  path.join(supportDir, 'browser/preview-oauth-gate')
const loginTimeoutMs = Number(process.env.QIU_MARKET_GATE_LOGIN_TIMEOUT_MS ?? 300_000)
const headless = process.env.QIU_MARKET_GATE_HEADLESS === 'true'
const aliasStabilityTimeoutMs = 45_000
const aliasStableSamples = 3

async function readPrivateJSON(file) {
  await requirePrivateRegularFile(file)
  return JSON.parse(await readFile(file, 'utf8'))
}

function validateReleaseState(state) {
  invariant(state?.schema_version === 1, 'release state schema mismatch')
  invariant(
    state?.phase === 'awaiting-production-auth',
    'release is not awaiting Production authentication',
  )
  invariant(/^[0-9a-f]{32}$/.test(state?.promotion_id ?? ''), 'promotion ID is invalid')
  invariant(
    /^dpl_[A-Za-z0-9]+$/.test(state?.candidate?.id ?? ''),
    'candidate deployment ID is invalid',
  )
  invariant(
    /^https:\/\/[A-Za-z0-9.-]+\.vercel\.app$/.test(state?.candidate?.url ?? ''),
    'candidate deployment URL is invalid',
  )
  invariant(
    /^[0-9a-f]{40}$/.test(state?.candidate?.commit ?? ''),
    'candidate release commit is invalid',
  )
  invariant(
    /^dpl_[A-Za-z0-9]+$/.test(state?.promoted?.id ?? ''),
    'promoted Production deployment ID is invalid',
  )
  invariant(
    /^https:\/\/[A-Za-z0-9.-]+\.vercel\.app$/.test(state?.promoted?.url ?? ''),
    'promoted Production deployment URL is invalid',
  )
  invariant(
    /^https:\/\/[A-Za-z0-9.-]+$/.test(state?.production_origin ?? ''),
    'Production origin is invalid',
  )
  invariant(
    typeof state?.promoted_at === 'string' && state.promoted_at.length > 0,
    'promotion timestamp is missing',
  )
  invariant(
    Number.isFinite(loginTimeoutMs) && loginTimeoutMs >= 60_000 && loginTimeoutMs <= 900_000,
    'login timeout must be between 60 and 900 seconds',
  )
  return state
}

async function browserFetch(page, endpoint, options = {}) {
  return page.evaluate(async ({ endpoint: target, options: requestOptions }) => {
    const response = await fetch(target, {
      method: requestOptions.method ?? 'GET',
      headers: requestOptions.headers ?? {},
      body: requestOptions.body === undefined
        ? undefined
        : JSON.stringify(requestOptions.body),
      credentials: 'same-origin',
      redirect: 'manual',
    })
    const text = await response.text()
    let body = null
    try {
      body = text ? JSON.parse(text) : null
    } catch {
      body = { raw: text }
    }
    return {
      status: response.status,
      body,
      provenance: {
        status: response.headers.get('x-qiu-market-provenance'),
        releaseCommit: response.headers.get('x-qiu-market-release-commit'),
        deploymentID: response.headers.get('x-qiu-market-deployment-id'),
        deploymentURL: response.headers.get('x-qiu-market-deployment-url'),
      },
    }
  }, { endpoint, options })
}

async function csrfToken(context, origin) {
  const cookies = await context.cookies(origin)
  const csrf = cookies.find((cookie) => cookie.name === 's78_trading_csrf')
  invariant(csrf?.value, 'CSRF cookie is missing')
  return csrf.value
}

async function browserWrite(page, context, origin, endpoint, body) {
  const csrf = await csrfToken(context, origin)
  return browserFetch(page, endpoint, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      'x-csrf-token': csrf,
    },
    body,
  })
}

function requireSuccess(result, label) {
  invariant(
    result.status >= 200 && result.status < 300,
    `${label} failed with HTTP ${result.status}: ${JSON.stringify(result.body)}`,
  )
  return result.body
}

async function waitForSession(page, origin, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  let callbackObservedAt = 0
  while (Date.now() < deadline) {
    const currentURL = page.url()
    if (currentURL.startsWith(`${origin}/`)) {
      const session = await browserFetch(page, '/api/v1/trading/session')
      if (session.status === 200) return session.body
      if (currentURL.startsWith(`${origin}/api/v1/trading/auth/github/callback`)) {
        callbackObservedAt ||= Date.now()
        if (Date.now() - callbackObservedAt >= 1_500) {
          const callbackBody = await page.locator('body').innerText({
            timeout: 1_000,
          }).catch(() => '')
          const callbackFailure = oauthCallbackError(callbackBody)
          if (callbackFailure) {
            throw new Error(
              `Production GitHub OAuth callback failed: ${callbackFailure.code}: ` +
              callbackFailure.message,
            )
          }
        }
      }
    }
    await page.waitForTimeout(500)
  }
  throw new Error('Production GitHub OAuth session was not created before the deadline')
}

async function waitForPromotedAlias(page, release) {
  const deadline = Date.now() + aliasStabilityTimeoutMs
  const expected = {
    deploymentID: release.promoted.id,
    deploymentURL: release.promoted.url,
    releaseCommit: release.candidate.commit,
  }
  let consecutive = 0
  let lastFailure = 'no response'
  while (Date.now() < deadline) {
    try {
      const capabilities = await browserFetch(
        page,
        '/api/v1/trading/auth/capabilities',
      )
      requireSuccess(capabilities, 'Production OAuth capabilities')
      validateReleaseProvenance(capabilities.provenance, expected)
      consecutive += 1
      if (consecutive >= aliasStableSamples) return capabilities.body
    } catch (error) {
      consecutive = 0
      lastFailure = error instanceof Error ? error.message : String(error)
    }
    await page.waitForTimeout(1_000)
  }
  throw new Error(
    `Production alias did not stabilize on the promoted deployment: ${lastFailure}`,
  )
}

function sequenceOf(status, label) {
  invariant(/^[0-9]+$/.test(status?.sequence ?? ''), `${label} sequence is invalid`)
  invariant(status?.state === 'ready', `${label} matching state is not ready`)
  invariant((status?.last_error ?? '') === '', `${label} matching state has an active error`)
  invariant(
    (status?.outbox_state ?? 'ready') === 'ready',
    `${label} outbox state is not ready`,
  )
  invariant(
    (status?.outbox_last_error ?? '') === '',
    `${label} outbox has an active error`,
  )
  return BigInt(status.sequence)
}

function loadDatabaseProof() {
  const proofScript = String.raw`
set -euo pipefail
repo_root="$1"
# shellcheck disable=SC1090
source "$repo_root/ops/macos/production-lib.sh"
qiu_load_production_environment "$repo_root"
qiu_require_private_environment
qiu_psql -c "
WITH ledger_imbalances AS (
  SELECT count(*) AS count
  FROM (
    SELECT market_id, transaction_id, asset
    FROM trading_ledger_entry
    GROUP BY market_id, transaction_id, asset
    HAVING sum(amount) <> 0
  ) imbalanced
),
market_state AS (
  SELECT
    market.current_sequence,
    event.sequence AS event_sequence,
    event.state_hash
  FROM trading_market market
  LEFT JOIN LATERAL (
    SELECT sequence, state_hash
    FROM trading_event_batch
    WHERE market_id=market.market_id
    ORDER BY sequence DESC
    LIMIT 1
  ) event ON true
  WHERE market.market_id='BTC-USDT'
)
SELECT jsonb_build_object(
  'ledger_imbalances', ledger_imbalances.count,
  'market_sequence', market_state.current_sequence,
  'event_sequence', market_state.event_sequence,
  'event_state_hash', market_state.state_hash
)::text
FROM ledger_imbalances
CROSS JOIN market_state
"
`
  const result = spawnSync('bash', ['-c', proofScript, 'qiu-production-proof', repoRoot], {
    cwd: repoRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 1024 * 1024,
  })
  invariant(
    result.status === 0,
    `Production database proof failed with exit ${result.status}: ${result.stderr.trim()}`,
  )
  const lines = result.stdout.trim().split('\n').filter(Boolean)
  invariant(lines.length === 1, 'Production database proof returned an unexpected row count')
  const proof = JSON.parse(lines[0])
  invariant(Number(proof.ledger_imbalances) === 0, 'Production ledger is imbalanced')
  invariant(/^[0-9]+$/.test(String(proof.market_sequence)), 'database market sequence is invalid')
  invariant(
    String(proof.event_sequence) === String(proof.market_sequence),
    'database market cursor does not match the latest event',
  )
  invariant(
    typeof proof.event_state_hash === 'string' &&
      /^[0-9a-f]{64}$/i.test(proof.event_state_hash),
    'latest event state hash is invalid',
  )
  return proof
}

async function main() {
  const release = validateReleaseState(await readPrivateJSON(releaseStateFile))
  const origin = release.production_origin.replace(/\/$/, '')
  const proxy = loopbackHTTPProxy()
  let context
  let apiContext

  try {
    await mkdir(browserProfile, { recursive: true, mode: 0o700 })
    await chmod(browserProfile, 0o700)
    const browserOptions = {
      channel: 'chrome',
      headless,
      viewport: { width: 1440, height: 900 },
    }
    if (proxy) browserOptions.proxy = proxy
    context = await chromium.launchPersistentContext(browserProfile, browserOptions)
    await context.clearCookies({ name: /^s78_trading_/ })
    const page = context.pages()[0] ?? await context.newPage()

    await page.goto(`${origin}/trade/BTC-USDT`, {
      waitUntil: 'domcontentloaded',
      timeout: 30_000,
    })
    const capabilities = await waitForPromotedAlias(page, release)
    invariant(capabilities.github_oauth_enabled === true, 'GitHub OAuth is not enabled')
    invariant(capabilities.local_login_enabled === false, 'local login must stay disabled')

    console.log('Complete GitHub authorization as qianqiu0404 if the Chrome window requests it.')
    await page.goto(`${origin}/api/v1/trading/auth/github/start`, {
      waitUntil: 'domcontentloaded',
      timeout: 30_000,
    })
    const session = await waitForSession(page, origin, loginTimeoutMs)
    const principal = session?.principal
    invariant(principal?.github_login === 'qianqiu0404', 'unexpected GitHub principal')
    invariant(principal?.account_id === 'github:qianqiu0404', 'unexpected trading account')
    invariant(principal?.admin === true, 'GitHub principal is not the virtual-fund admin')

    const cookies = await context.cookies(origin)
    const sessionCookie = cookies.find((cookie) => cookie.name === 's78_trading_session')
    const csrfCookie = cookies.find((cookie) => cookie.name === 's78_trading_csrf')
    invariant(
      sessionCookie?.secure &&
        sessionCookie.httpOnly &&
        sessionCookie.sameSite === 'Strict',
      'session cookie is not Secure, HttpOnly, SameSite=Strict',
    )
    invariant(
      csrfCookie?.secure &&
        !csrfCookie.httpOnly &&
        csrfCookie.sameSite === 'Strict',
      'CSRF cookie is not Secure, readable, SameSite=Strict',
    )

    const missingCSRF = await browserFetch(page, '/api/v1/trading/admin/fund', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: {
        request_id: requestID('production-csrf-reject'),
        account_id: principal.account_id,
        asset: 'USDT',
        amount: '0.000001',
      },
    })
    invariant(missingCSRF.status === 403, `missing CSRF was not rejected: ${missingCSRF.status}`)

    const apiContextOptions = { storageState: await context.storageState() }
    if (proxy) apiContextOptions.proxy = proxy
    apiContext = await request.newContext(apiContextOptions)
    const originRejected = await apiContext.post(
      `${origin}/api/v1/trading/admin/fund`,
      {
        headers: {
          origin: 'https://attacker.invalid',
          'x-csrf-token': csrfCookie.value,
          'content-type': 'application/json',
        },
        data: {
          request_id: requestID('production-origin-reject'),
          account_id: principal.account_id,
          asset: 'USDT',
          amount: '0.000001',
        },
        maxRedirects: 0,
      },
    )
    invariant(
      originRejected.status() === 403,
      `bad Origin was not rejected: ${originRejected.status()}`,
    )

    const statusBefore = requireSuccess(
      await browserFetch(page, '/api/v1/trading/markets/BTC-USDT/status'),
      'Production status before minimal write',
    )
    const sequenceBefore = sequenceOf(statusBefore, 'pre-write')
    const balancesBefore = requireSuccess(
      await browserFetch(page, '/api/v1/trading/balances'),
      'Production balances before minimal write',
    )
    const usdtBefore = parseDecimalAtoms(balanceAvailable(balancesBefore, 'USDT'), 6)

    const fundRequestID = requestID('production-fund')
    const fundBody = {
      request_id: fundRequestID,
      account_id: principal.account_id,
      asset: 'USDT',
      amount: '0.000001',
    }
    const firstFund = requireSuccess(
      await browserWrite(
        page,
        context,
        origin,
        '/api/v1/trading/admin/fund',
        fundBody,
      ),
      'Production minimal virtual fund',
    )
    const balancesAfterFirst = requireSuccess(
      await browserFetch(page, '/api/v1/trading/balances'),
      'Production balances after minimal write',
    )
    const usdtAfterFirst = parseDecimalAtoms(
      balanceAvailable(balancesAfterFirst, 'USDT'),
      6,
    )
    invariant(usdtAfterFirst === usdtBefore + 1n, 'minimal virtual fund was not credited once')
    const statusAfterFirst = requireSuccess(
      await browserFetch(page, '/api/v1/trading/markets/BTC-USDT/status'),
      'Production status after minimal write',
    )
    const sequenceAfterFirst = sequenceOf(statusAfterFirst, 'post-write')
    invariant(sequenceAfterFirst > sequenceBefore, 'minimal virtual fund did not advance the stream')

    const replayFund = requireSuccess(
      await browserWrite(
        page,
        context,
        origin,
        '/api/v1/trading/admin/fund',
        fundBody,
      ),
      'Production same-ID virtual fund replay',
    )
    invariant(
      canonicalJSON(replayFund) === canonicalJSON(firstFund),
      'same request ID did not return the original result',
    )
    const balancesAfterReplay = requireSuccess(
      await browserFetch(page, '/api/v1/trading/balances'),
      'Production balances after same-ID replay',
    )
    invariant(
      parseDecimalAtoms(balanceAvailable(balancesAfterReplay, 'USDT'), 6) === usdtAfterFirst,
      'same request ID duplicated the virtual credit',
    )
    const statusAfterReplay = requireSuccess(
      await browserFetch(page, '/api/v1/trading/markets/BTC-USDT/status'),
      'Production status after same-ID replay',
    )
    const sequenceAfterReplay = sequenceOf(statusAfterReplay, 'post-replay')
    invariant(
      sequenceAfterReplay === sequenceAfterFirst,
      'same request ID advanced the event stream twice',
    )

    const databaseProof = loadDatabaseProof()
    invariant(
      BigInt(databaseProof.market_sequence) === sequenceAfterReplay,
      'running market sequence does not match the authoritative database',
    )

    const logout = await browserWrite(
      page,
      context,
      origin,
      '/api/v1/trading/auth/logout',
      {},
    )
    invariant(logout.status === 204, `Production logout returned ${logout.status}`)
    const staleSession = await browserFetch(page, '/api/v1/trading/session')
    invariant(
      staleSession.status === 401,
      `stale Production session returned ${staleSession.status}`,
    )

    const completedAt = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
    await writePrivateJSON(evidenceFile, {
      schema_version: 1,
      promotion_id: release.promotion_id,
      deployment_id: release.candidate.id,
      deployment_commit: release.candidate.commit,
      production_deployment_id: release.promoted.id,
      production_deployment_url: release.promoted.url,
      production_login: true,
      github_login: principal.github_login,
      secure_cookie: true,
      csrf_rejected: true,
      origin_rejected: true,
      minimal_virtual_write_reconciled: true,
      request_id: fundRequestID,
      same_request_id_replay_equal: true,
      ledger_balanced: Number(databaseProof.ledger_imbalances) === 0,
      state_hash_consistent: true,
      production_logout_204: true,
      stale_production_session_401: true,
      proof: {
        sequence_before: sequenceBefore.toString(),
        sequence_after_write: sequenceAfterFirst.toString(),
        sequence_after_replay: sequenceAfterReplay.toString(),
        database_market_sequence: String(databaseProof.market_sequence),
        database_event_sequence: String(databaseProof.event_sequence),
        database_event_state_hash: databaseProof.event_state_hash,
        ledger_imbalance_count: Number(databaseProof.ledger_imbalances),
        virtual_credit_atoms: '1',
      },
      promoted_at: release.promoted_at,
      completed_at: completedAt,
    })
    console.log('Qiu Market Production OAuth and minimal-write evidence passed.')
  } finally {
    await apiContext?.dispose()
    await context?.close()
  }
}

main().catch((error) => {
  console.error(`Production OAuth Gate failed: ${error.message}`)
  process.exitCode = 1
})
