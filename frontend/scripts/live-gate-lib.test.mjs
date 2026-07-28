import assert from 'node:assert/strict'
import { mkdtemp, readFile, stat } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'

import {
  canonicalJSON,
  formatDecimalAtoms,
  parseDecimalAtoms,
  selectRestingSellPrice,
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
