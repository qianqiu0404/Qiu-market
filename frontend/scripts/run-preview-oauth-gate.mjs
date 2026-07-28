#!/usr/bin/env node

import { spawnSync } from 'node:child_process'
import { chmod, mkdir, readFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { chromium } from '@playwright/test'

import {
  balanceAvailable,
  canonicalJSON,
  invariant,
  parseDecimalAtoms,
  requestID,
  requirePrivateRegularFile,
  selectRestingSellPrice,
  validateWindowState,
  writePrivateJSON,
} from './live-gate-lib.mjs'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(scriptDirectory, '../..')
const supportDir = process.env.QIU_MARKET_SUPPORT_DIR ??
  path.join(os.homedir(), 'Library/Application Support/Qiu Market')
const observationsDir = path.join(supportDir, 'observations')
const stateFile = process.env.QIU_MARKET_PREVIEW_OAUTH_WINDOW_STATE ??
  path.join(supportDir, 'preview-oauth-window/active.json')
const closeReportFile = process.env.QIU_MARKET_PREVIEW_OAUTH_WINDOW_REPORT ??
  path.join(supportDir, 'preview-oauth-window/last.json')
const precloseEvidenceFile = process.env.QIU_MARKET_PREVIEW_OAUTH_PRECLOSE_EVIDENCE_FILE ??
  path.join(observationsDir, 'preview-oauth-preclose-evidence.json')
const finalEvidenceFile = process.env.QIU_MARKET_PREVIEW_OAUTH_EVIDENCE_FILE ??
  path.join(observationsDir, 'preview-oauth-evidence.json')
const screenshotFile = process.env.QIU_MARKET_PREVIEW_OAUTH_SCREENSHOT_FILE ??
  path.join(observationsDir, 'preview-oauth-trade.png')
const browserProfile = process.env.QIU_MARKET_GATE_BROWSER_PROFILE ??
  path.join(supportDir, 'browser/preview-oauth-gate')
const loginTimeoutMs = Number(process.env.QIU_MARKET_GATE_LOGIN_TIMEOUT_MS ?? 300_000)
const headless = process.env.QIU_MARKET_GATE_HEADLESS === 'true'

function parseArguments(argv) {
  const result = {}
  for (let index = 0; index < argv.length; index += 2) {
    const name = argv[index]
    const value = argv[index + 1]
    invariant(value, `missing value for ${name}`)
    if (name === '--deployment-id') result.deploymentID = value
    else if (name === '--deployment-url') result.deploymentURL = value.replace(/\/$/, '')
    else if (name === '--commit') result.deploymentCommit = value.toLowerCase()
    else throw new Error(`unsupported argument: ${name}`)
  }
  invariant(/^dpl_[A-Za-z0-9]+$/.test(result.deploymentID ?? ''), 'invalid deployment ID')
  invariant(
    /^https:\/\/[A-Za-z0-9.-]+\.vercel\.app$/.test(result.deploymentURL ?? ''),
    'invalid immutable Preview URL',
  )
  invariant(/^[0-9a-f]{40}$/.test(result.deploymentCommit ?? ''), 'invalid release commit')
  invariant(
    Number.isFinite(loginTimeoutMs) && loginTimeoutMs >= 60_000 && loginTimeoutMs <= 900_000,
    'login timeout must be between 60 and 900 seconds',
  )
  return result
}

async function readJSON(file) {
  await requirePrivateRegularFile(file)
  return JSON.parse(await readFile(file, 'utf8'))
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
    return { status: response.status, body }
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

async function waitForPreviewOrigin(page, origin, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (page.url().startsWith(`${origin}/`)) return
    await page.waitForTimeout(500)
  }
  throw new Error('Vercel Authentication was not completed before the deadline')
}

async function waitForSession(page, origin, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (page.url().startsWith(`${origin}/`)) {
      const session = await browserFetch(page, '/api/v1/trading/session')
      if (session.status === 200) return session.body
    }
    await page.waitForTimeout(500)
  }
  throw new Error('GitHub OAuth session was not created before the deadline')
}

async function committedResponseLostOnce(page, context, origin, endpoint, body) {
  const absoluteURL = `${origin}${endpoint}`
  let committed = null
  let upstreamFailure = null
  await page.route(absoluteURL, async (route) => {
    try {
      const upstream = await route.fetch()
      const text = await upstream.text()
      let parsed = null
      try {
        parsed = text ? JSON.parse(text) : null
      } catch {
        parsed = { raw: text }
      }
      if (!upstream.ok()) {
        upstreamFailure = new Error(
          `upstream write failed with HTTP ${upstream.status()}: ${text}`,
        )
      } else {
        committed = { status: upstream.status(), body: parsed }
      }
      await route.fulfill({
        status: upstream.ok() ? 504 : upstream.status(),
        contentType: 'application/json',
        body: upstream.ok()
          ? JSON.stringify({
            code: 'backend_timeout',
            message: 'fixture dropped the committed response',
          })
          : text,
      })
    } catch (error) {
      upstreamFailure = error
      await route.fulfill({
        status: 502,
        contentType: 'application/json',
        body: JSON.stringify({ code: 'fault_injection_failed' }),
      })
    }
  }, { times: 1 })

  const visible = await browserWrite(page, context, origin, endpoint, body)
  invariant(!upstreamFailure, upstreamFailure?.message ?? 'upstream write failed')
  invariant(committed, 'fault injector did not observe a committed response')
  invariant(visible.status === 504, `browser did not observe unknown outcome: ${visible.status}`)

  const replay = await browserWrite(page, context, origin, endpoint, body)
  requireSuccess(replay, 'same-ID authoritative replay')
  invariant(
    canonicalJSON(replay.body) === canonicalJSON(committed.body),
    'same-ID replay did not return the committed result',
  )
  return { committed: committed.body, replay: replay.body }
}

async function runSecurityChecks(page, context, origin, principal) {
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
      request_id: requestID('csrf-reject'),
      account_id: principal.account_id,
      asset: 'USDT',
      amount: '0.000001',
    },
  })
  invariant(missingCSRF.status === 403, `missing CSRF was not rejected: ${missingCSRF.status}`)

  const originRejected = await context.request.post(
    `${origin}/api/v1/trading/admin/fund`,
    {
      headers: {
        origin: 'https://attacker.invalid',
        'x-csrf-token': csrfCookie.value,
        'content-type': 'application/json',
      },
      data: {
        request_id: requestID('origin-reject'),
        account_id: principal.account_id,
        asset: 'USDT',
        amount: '0.000001',
      },
      maxRedirects: 0,
    },
  )
  invariant(originRejected.status() === 403, `bad Origin was not rejected: ${originRejected.status()}`)
  return { secureCookie: true, csrfRejected: true, originRejected: true }
}

async function runUnknownWriteChecks(page, context, origin, principal) {
  const setupFundID = requestID('setup-btc')
  requireSuccess(
    await browserWrite(
      page,
      context,
      origin,
      '/api/v1/trading/admin/fund',
      {
        request_id: setupFundID,
        account_id: principal.account_id,
        asset: 'BTC',
        amount: '0.002',
      },
    ),
    'virtual BTC setup funding',
  )
  requireSuccess(
    await browserWrite(
      page,
      context,
      origin,
      '/api/v1/trading/admin/fund',
      {
        request_id: requestID('setup-usdt'),
        account_id: principal.account_id,
        asset: 'USDT',
        amount: '10',
      },
    ),
    'virtual USDT setup funding',
  )

  const beforeBalances = requireSuccess(
    await browserFetch(page, '/api/v1/trading/balances'),
    'balances before unknown fund',
  )
  const beforeUSDT = parseDecimalAtoms(balanceAvailable(beforeBalances, 'USDT'), 6)
  const fundID = requestID('unknown-fund')
  const fundBody = {
    request_id: fundID,
    account_id: principal.account_id,
    asset: 'USDT',
    amount: '1',
  }
  await committedResponseLostOnce(
    page,
    context,
    origin,
    '/api/v1/trading/admin/fund',
    fundBody,
  )
  const afterBalances = requireSuccess(
    await browserFetch(page, '/api/v1/trading/balances'),
    'balances after unknown fund',
  )
  const afterUSDT = parseDecimalAtoms(balanceAvailable(afterBalances, 'USDT'), 6)
  invariant(afterUSDT === beforeUSDT + 1_000_000n, 'unknown fund was not credited exactly once')
  requireSuccess(
    await browserWrite(
      page,
      context,
      origin,
      '/api/v1/trading/admin/fund',
      fundBody,
    ),
    'second same-ID fund replay',
  )
  const replayBalances = requireSuccess(
    await browserFetch(page, '/api/v1/trading/balances'),
    'balances after second fund replay',
  )
  invariant(
    parseDecimalAtoms(balanceAvailable(replayBalances, 'USDT'), 6) === afterUSDT,
    'same-ID fund replay duplicated virtual credit',
  )

  const orderBook = requireSuccess(
    await browserFetch(page, '/api/v1/trading/markets/BTC-USDT/orderbook?levels=5'),
    'orderbook for resting order',
  )
  const clientOrderID = requestID('unknown-submit')
  let cleanupOrderID = ''
  const submitBody = {
    client_order_id: clientOrderID,
    side: 'sell',
    type: 'limit',
    time_in_force: 'gtc',
    post_only: true,
    price: selectRestingSellPrice(orderBook),
    quantity: '0.0001',
    quote_budget: '',
  }
  try {
    const submit = await committedResponseLostOnce(
      page,
      context,
      origin,
      '/api/v1/trading/orders',
      submitBody,
    )
    const orderID = submit.replay?.order_id
    invariant(typeof orderID === 'string' && orderID.length > 0, 'submit replay omitted order ID')
    cleanupOrderID = orderID
    const orders = requireSuccess(
      await browserFetch(page, '/api/v1/trading/orders?limit=100&open_only=false'),
      'authoritative order list',
    )
    const order = orders?.orders?.find((item) => item?.client_order_id === clientOrderID)
    invariant(order?.id === orderID && order.status === 'open', 'submitted order is not authoritatively open')

    const cancelID = requestID('unknown-cancel')
    const cancelEndpoint = `/api/v1/trading/orders/${encodeURIComponent(orderID)}/cancel`
    await committedResponseLostOnce(
      page,
      context,
      origin,
      cancelEndpoint,
      { request_id: cancelID },
    )
    const canceled = requireSuccess(
      await browserFetch(
        page,
        `/api/v1/trading/orders/${encodeURIComponent(orderID)}`,
      ),
      'authoritative canceled order',
    )
    invariant(canceled?.status === 'canceled', 'cancel result is not authoritatively terminal')
    cleanupOrderID = ''

    return {
      submitUnknownReconciled: true,
      cancelUnknownReconciled: true,
      fundUnknownReconciled: true,
      fundRequestID: fundID,
      submitRequestID: clientOrderID,
      cancelRequestID: cancelID,
      orderID,
    }
  } catch (error) {
    if (cleanupOrderID) {
      try {
        await browserWrite(
          page,
          context,
          origin,
          `/api/v1/trading/orders/${encodeURIComponent(cleanupOrderID)}/cancel`,
          { request_id: requestID('cleanup-cancel') },
        )
      } catch {
        // The outer managed abort restores configuration; report the original failure.
      }
    }
    throw error
  }
}

function runManagedCommand(script, args, extraEnvironment = {}) {
  const result = spawnSync('bash', [script, ...args], {
    cwd: repoRoot,
    env: { ...process.env, ...extraEnvironment },
    stdio: 'inherit',
  })
  invariant(result.status === 0, `${path.basename(script)} failed with exit ${result.status}`)
}

async function main() {
  const identity = parseArguments(process.argv.slice(2))
  const state = validateWindowState(await readJSON(stateFile), identity)
  const manager = path.join(repoRoot, 'ops/macos/manage-preview-oauth-window.sh')
  const verifier = path.join(repoRoot, 'ops/macos/verify-preview-gate.sh')
  let context
  let managedCloseCompleted = false
  let callbackURL = ''
  let callbackResponses = 0
  const callbackStatuses = []
  const consoleErrors = []

  try {
    await mkdir(browserProfile, { recursive: true, mode: 0o700 })
    await chmod(browserProfile, 0o700)
    context = await chromium.launchPersistentContext(browserProfile, {
      channel: 'chrome',
      headless,
      viewport: { width: 1440, height: 900 },
    })
    await context.clearCookies({ name: /^s78_trading_/ })
    const pages = context.pages()
    const page = pages[0] ?? await context.newPage()
    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors.push(message.text())
    })
    page.on('request', (request) => {
      const url = new URL(request.url())
      if (
        url.origin === identity.deploymentURL &&
        url.pathname === '/api/v1/trading/auth/github/callback'
      ) {
        callbackURL = request.url()
      }
    })
    page.on('response', (response) => {
      const url = new URL(response.url())
      if (
        url.origin === identity.deploymentURL &&
        url.pathname === '/api/v1/trading/auth/github/callback'
      ) {
        callbackResponses += 1
        callbackStatuses.push(response.status())
      }
    })

    console.log('Complete Vercel access in the opened Chrome window if requested.')
    await page.goto(`${identity.deploymentURL}/trade/BTC-USDT`, {
      waitUntil: 'domcontentloaded',
      timeout: 30_000,
    })
    await waitForPreviewOrigin(page, identity.deploymentURL, loginTimeoutMs)

    const capabilities = requireSuccess(
      await browserFetch(page, '/api/v1/trading/auth/capabilities'),
      'OAuth capabilities',
    )
    invariant(capabilities.github_oauth_enabled === true, 'GitHub OAuth is not enabled')
    invariant(capabilities.local_login_enabled === false, 'local login must stay disabled')

    console.log('Complete GitHub authorization as qianqiu0404 if requested.')
    await page.goto(`${identity.deploymentURL}/api/v1/trading/auth/github/start`, {
      waitUntil: 'domcontentloaded',
      timeout: 30_000,
    })
    const session = await waitForSession(page, identity.deploymentURL, loginTimeoutMs)
    const principal = session?.principal
    invariant(principal?.github_login === 'qianqiu0404', 'unexpected GitHub principal')
    invariant(principal?.account_id === 'github:qianqiu0404', 'unexpected trading account')
    invariant(principal?.admin === true, 'GitHub principal is not the virtual-fund admin')
    invariant(
      callbackURL &&
        callbackResponses === 1 &&
        callbackStatuses.length === 1 &&
        callbackStatuses[0] === 302,
      `OAuth callback was not consumed exactly once: ${callbackStatuses.join(',')}`,
    )

    consoleErrors.length = 0
    await page.goto(`${identity.deploymentURL}/trade/BTC-USDT`, {
      waitUntil: 'domcontentloaded',
      timeout: 30_000,
    })
    await page.waitForTimeout(2_000)
    const visualState = await page.evaluate(() => ({
      bodyText: document.body.innerText.trim(),
      errorOverlay: Boolean(document.querySelector(
        '.vite-error-overlay, #webpack-dev-server-client-overlay, [data-nextjs-dialog]',
      )),
    }))
    invariant(visualState.bodyText.length > 100, 'Trade page rendered no meaningful content')
    invariant(visualState.bodyText.includes('BTC / USDT'), 'Trade page identity is missing')
    invariant(!visualState.errorOverlay, 'Trade page rendered a framework error overlay')
    invariant(consoleErrors.length === 0, `Trade page console errors: ${consoleErrors.join(' | ')}`)
    await mkdir(path.dirname(screenshotFile), { recursive: true, mode: 0o700 })
    await chmod(path.dirname(screenshotFile), 0o700)
    await page.screenshot({ path: screenshotFile, fullPage: true })
    await chmod(screenshotFile, 0o600)

    const replay = await context.request.get(callbackURL, { maxRedirects: 0 })
    invariant(replay.status() === 400, `OAuth callback replay was not rejected: ${replay.status()}`)

    const security = await runSecurityChecks(
      page,
      context,
      identity.deploymentURL,
      principal,
    )
    const unknown = await runUnknownWriteChecks(
      page,
      context,
      identity.deploymentURL,
      principal,
    )

    const logout = await browserWrite(
      page,
      context,
      identity.deploymentURL,
      '/api/v1/trading/auth/logout',
      {},
    )
    invariant(logout.status === 204, `Preview logout returned ${logout.status}`)

    const precloseEvidence = {
      schema_version: 1,
      deployment_id: identity.deploymentID,
      deployment_commit: identity.deploymentCommit,
      window_id: state.window_id,
      window_opened_at: state.opened_at,
      callback_single_use: true,
      secure_cookie: security.secureCookie,
      csrf_rejected: security.csrfRejected,
      origin_rejected: security.originRejected,
      submit_unknown_reconciled: unknown.submitUnknownReconciled,
      cancel_unknown_reconciled: unknown.cancelUnknownReconciled,
      fund_unknown_reconciled: unknown.fundUnknownReconciled,
      preview_logout_204: true,
      principal_account_id: principal.account_id,
      fund_request_id: unknown.fundRequestID,
      submit_request_id: unknown.submitRequestID,
      cancel_request_id: unknown.cancelRequestID,
      order_id: unknown.orderID,
      visual_trade_page: true,
      console_error_count: 0,
      completed_at: new Date().toISOString(),
    }
    await writePrivateJSON(precloseEvidenceFile, precloseEvidence)

    runManagedCommand(manager, ['close'], {
      QIU_MARKET_PREVIEW_OAUTH_PRECLOSE_EVIDENCE_FILE: precloseEvidenceFile,
    })
    managedCloseCompleted = true

    const staleSession = await browserFetch(page, '/api/v1/trading/session')
    invariant(staleSession.status === 401, `stale Preview session returned ${staleSession.status}`)
    const staleWrite = await browserFetch(page, '/api/v1/trading/admin/fund', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-csrf-token': 'expired-after-preview-logout',
      },
      body: {
        request_id: requestID('stale-preview-write'),
        account_id: principal.account_id,
        asset: 'USDT',
        amount: '0.000001',
      },
    })
    invariant(staleWrite.status === 401, `stale Preview write returned ${staleWrite.status}`)

    const closeReport = await readJSON(closeReportFile)
    invariant(closeReport.status === 'closed_after_verified_logout', 'managed close was not verified')
    invariant(closeReport.window_id === state.window_id, 'managed close window mismatch')
    invariant(
      closeReport.deployment_id === identity.deploymentID &&
        closeReport.deployment_commit === identity.deploymentCommit,
      'managed close release mismatch',
    )

    await writePrivateJSON(finalEvidenceFile, {
      schema_version: 2,
      deployment_id: identity.deploymentID,
      deployment_commit: identity.deploymentCommit,
      window_id: state.window_id,
      window_opened_at: state.opened_at,
      maintenance_closed_at: closeReport.completed_at,
      callback_single_use: true,
      secure_cookie: true,
      csrf_rejected: true,
      origin_rejected: true,
      submit_unknown_reconciled: true,
      cancel_unknown_reconciled: true,
      fund_unknown_reconciled: true,
      preview_logout_204: true,
      stale_preview_session_401: true,
      stale_preview_write_401: true,
      principal_account_id: principal.account_id,
      fund_request_id: unknown.fundRequestID,
      submit_request_id: unknown.submitRequestID,
      cancel_request_id: unknown.cancelRequestID,
      order_id: unknown.orderID,
      visual_trade_page: true,
      console_error_count: 0,
      completed_at: new Date().toISOString(),
    })

    runManagedCommand(
      verifier,
      [
        '--deployment-id', identity.deploymentID,
        '--deployment-url', identity.deploymentURL,
        '--commit', identity.deploymentCommit,
      ],
      {
        QIU_MARKET_PREVIEW_OAUTH_EVIDENCE_FILE: finalEvidenceFile,
        QIU_MARKET_PREVIEW_OAUTH_WINDOW_REPORT: closeReportFile,
      },
    )
    console.log('Qiu Market Preview OAuth Gate 2C passed with live browser evidence.')
  } catch (error) {
    if (!managedCloseCompleted) {
      try {
        await requirePrivateRegularFile(stateFile)
        const result = spawnSync(
          'bash',
          [path.join(repoRoot, 'ops/macos/manage-preview-oauth-window.sh'), 'abort'],
          { cwd: repoRoot, env: process.env, stdio: 'inherit' },
        )
        if (result.status !== 0) {
          console.error('Managed OAuth abort could not be verified; inspect private state immediately.')
        }
      } catch {
        // No active state remains; close may already have completed.
      }
    }
    throw error
  } finally {
    await context?.close()
  }
}

main().catch((error) => {
  console.error(`Preview OAuth Gate failed: ${error.message}`)
  process.exitCode = 1
})
