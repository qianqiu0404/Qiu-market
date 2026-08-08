import assert from 'node:assert/strict'
import { mkdtemp, readFile, stat } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'

import {
  canonicalJSON,
  formatDecimalAtoms,
  isExpectedVercelToolbarCSPError,
  isOAuthRedirectNavigationAbort,
  loopbackHTTPProxy,
  oauthCallbackError,
  parseDecimalAtoms,
  selectRestingSellPrice,
  validateReleaseProvenance,
  validateWindowState,
  writePrivateJSON,
} from './live-gate-lib.mjs'

test('decimal helpers preserve asset atoms without float arithmetic', () => {
  assert.equal(parseDecimalAtoms('1.000001', 6), 1_000_001n)
  assert.equal(formatDecimalAtoms(1_000_001n, 6), '1.000001')
  assert.equal(formatDecimalAtoms(200_000_00n, 2), '200000')
  assert.throws(() => parseDecimalAtoms('1.0000001', 6), /too many decimal/)
})

test('canonical JSON ignores object insertion order but not values', () => {
  assert.equal(
    canonicalJSON({ sequence: '4', result: { b: 2, a: 1 } }),
    canonicalJSON({ result: { a: 1, b: 2 }, sequence: '4' }),
  )
  assert.notEqual(canonicalJSON({ status: 'open' }), canonicalJSON({ status: 'canceled' }))
})

test('OAuth callback replay uses one same-origin browser request', async () => {
  const collector = await readFile(
    new URL('./run-preview-oauth-gate.mjs', import.meta.url),
    'utf8',
  )
  assert.equal(
    [...collector.matchAll(/browserFetch\(page, callbackURL\)/g)].length,
    1,
  )
  assert.doesNotMatch(collector, /context\.request\.get\(callbackURL/)
  assert.match(collector, /if \(proxy\) browserOptions\.proxy = proxy/)
  assert.match(collector, /if \(proxy\) apiContextOptions\.proxy = proxy/)
  assert.match(
    collector,
    /auth\/github\/start`, \{[\s\S]*?timeout: loginTimeoutMs/,
  )
})

test('Production gate replays one persisted virtual-fund request and binds database proof', async () => {
  const collector = await readFile(
    new URL('./run-production-auth-gate.mjs', import.meta.url),
    'utf8',
  )
  assert.equal(
    [...collector.matchAll(/'\/api\/v1\/trading\/admin\/fund',\n\s+fundBody/g)].length,
    2,
  )
  assert.match(collector, /canonicalJSON\(replayFund\) === canonicalJSON\(firstFund\)/)
  assert.match(collector, /sequenceAfterReplay === sequenceAfterFirst/)
  assert.match(collector, /BigInt\(databaseProof\.market_sequence\) === sequenceAfterReplay/)
  assert.match(collector, /HAVING sum\(amount\) <> 0/)
  assert.equal(
    [...collector.matchAll(/page\.goto\(`\$\{origin\}\/api\/v1\/trading\/auth\/github\/start`/g)]
      .length,
    1,
  )
  assert.match(collector, /replace\(\/\\\.\\d\{3\}Z\$\/, 'Z'\)/)
  assert.match(collector, /waitForPromotedAlias/)
  assert.match(collector, /oauthCallbackError/)
})

test('API probes accept only credential-free loopback HTTP proxies', () => {
  assert.deepEqual(
    loopbackHTTPProxy({ HTTPS_PROXY: 'http://127.0.0.1:7890' }),
    { server: 'http://127.0.0.1:7890' },
  )
  assert.deepEqual(
    loopbackHTTPProxy({ https_proxy: 'http://localhost:7890' }),
    { server: 'http://localhost:7890' },
  )
  assert.equal(loopbackHTTPProxy({ HTTPS_PROXY: 'https://127.0.0.1:7890' }), undefined)
  assert.equal(loopbackHTTPProxy({ HTTPS_PROXY: 'http://proxy.example:7890' }), undefined)
  assert.equal(loopbackHTTPProxy({ HTTPS_PROXY: 'http://user:pass@127.0.0.1:7890' }), undefined)
  assert.equal(loopbackHTTPProxy({}), undefined)
})

test('resting sell price stays far above the current best ask', () => {
  assert.equal(selectRestingSellPrice({ asks: [{ price: '65000.01' }] }), '200000')
  assert.equal(selectRestingSellPrice({ asks: [{ price: '150000.00' }] }), '300000')
  assert.equal(selectRestingSellPrice({ asks: [] }), '200000')
})

test('managed window validation binds all release identity fields', () => {
  const expected = {
    deploymentID: 'dpl_Fixture123',
    deploymentURL: 'https://fixture.vercel.app',
    deploymentCommit: 'a'.repeat(40),
  }
  const state = {
    schema_version: 1,
    phase: 'open',
    deployment_id: expected.deploymentID,
    deployment_url: expected.deploymentURL,
    deployment_commit: expected.deploymentCommit,
    window_id: '0123456789abcdef0123456789abcdef',
    opened_at: '2026-07-28T00:00:00Z',
  }
  assert.equal(validateWindowState(state, expected), state)
  assert.throws(
    () => validateWindowState({ ...state, deployment_id: 'dpl_Other' }, expected),
    /deployment ID mismatch/,
  )
})

test('release provenance binds the active alias to the promoted clone', () => {
  const expected = {
    deploymentID: 'dpl_Promoted123',
    deploymentURL: 'https://qiu-market-promoted.vercel.app',
    releaseCommit: 'a'.repeat(40),
  }
  const observed = {
    status: 'VERIFIED',
    deploymentID: expected.deploymentID,
    deploymentURL: expected.deploymentURL,
    releaseCommit: expected.releaseCommit,
  }
  assert.equal(validateReleaseProvenance(observed, expected), observed)
  assert.throws(
    () => validateReleaseProvenance(
      { ...observed, deploymentID: 'dpl_Previous123' },
      expected,
    ),
    /deployment ID mismatch/,
  )
})

test('OAuth callback errors are detected without retaining code or state', () => {
  assert.deepEqual(
    oauthCallbackError(JSON.stringify({
      code: 'invalid_oauth_state',
      message: 'OAuth state is invalid or expired',
      state: 'do-not-retain',
    })),
    {
      code: 'invalid_oauth_state',
      message: 'OAuth state is invalid or expired',
    },
  )
  assert.equal(oauthCallbackError('not json'), undefined)
  assert.equal(
    oauthCallbackError(JSON.stringify({ code: 'validation_failed', message: 'bad' })),
    undefined,
  )
})

test('only the exact Vercel Toolbar CSP error is classified as platform noise', () => {
  const expected = "Loading the script 'https://vercel.live/_next-live/feedback/feedback.js' " +
    "violates the following Content Security Policy directive: \"script-src 'self'\". " +
    'The action has been blocked.'
  assert.equal(isExpectedVercelToolbarCSPError(expected), true)
  assert.equal(
    isExpectedVercelToolbarCSPError(expected.replace('vercel.live', 'attacker.invalid')),
    false,
  )
  assert.equal(
    isExpectedVercelToolbarCSPError(expected.replace('feedback.js', 'other.js')),
    false,
  )
  assert.equal(isExpectedVercelToolbarCSPError('application console error'), false)
})

test('OAuth redirect navigation aborts are deferred to the session authority', () => {
  assert.equal(
    isOAuthRedirectNavigationAbort(
      new Error('page.goto: net::ERR_ABORTED at https://fixture.vercel.app/callback'),
    ),
    true,
  )
  assert.equal(isOAuthRedirectNavigationAbort(new Error('page.goto: timeout')), false)
  assert.equal(isOAuthRedirectNavigationAbort('net::ERR_ABORTED'), false)
})

test('private JSON writer uses an atomic 0600 destination', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'qiu-live-gate-'))
  const output = path.join(directory, 'evidence.json')
  await writePrivateJSON(output, { passed: false, reason: 'fixture' })
  assert.deepEqual(JSON.parse(await readFile(output, 'utf8')), {
    passed: false,
    reason: 'fixture',
  })
  assert.equal((await stat(output)).mode & 0o777, 0o600)
})
