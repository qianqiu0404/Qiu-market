import { randomUUID } from 'node:crypto'
import { chmod, lstat, mkdir, rename, writeFile } from 'node:fs/promises'
import path from 'node:path'

export function invariant(condition, message) {
  if (!condition) throw new Error(message)
}

export function parseDecimalAtoms(value, decimals) {
  invariant(Number.isInteger(decimals) && decimals >= 0, 'invalid decimal scale')
  const match = /^([0-9]+)(?:\.([0-9]+))?$/.exec(String(value))
  invariant(match, `invalid non-negative decimal: ${value}`)
  const fraction = match[2] ?? ''
  invariant(fraction.length <= decimals, `too many decimal places: ${value}`)
  return BigInt(match[1]) * (10n ** BigInt(decimals)) +
    BigInt((fraction + '0'.repeat(decimals)).slice(0, decimals) || '0')
}

export function formatDecimalAtoms(atoms, decimals) {
  invariant(typeof atoms === 'bigint' && atoms >= 0n, 'atoms must be non-negative')
  invariant(Number.isInteger(decimals) && decimals >= 0, 'invalid decimal scale')
  if (decimals === 0) return atoms.toString()
  const scale = 10n ** BigInt(decimals)
  const whole = atoms / scale
  const fraction = (atoms % scale).toString().padStart(decimals, '0')
  return `${whole}.${fraction}`.replace(/\.?0+$/, '')
}

export function canonicalJSON(value) {
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJSON(item)).join(',')}]`
  }
  if (value && typeof value === 'object') {
    return `{${Object.keys(value).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJSON(value[key])}`,
    ).join(',')}}`
  }
  return JSON.stringify(value)
}

export function selectRestingSellPrice(orderBook) {
  const bestAsk = orderBook?.asks?.[0]?.price
  const floor = parseDecimalAtoms('200000', 2)
  if (!bestAsk) return formatDecimalAtoms(floor, 2)
  const doubled = parseDecimalAtoms(bestAsk, 2) * 2n
  return formatDecimalAtoms(doubled > floor ? doubled : floor, 2)
}

export function requestID(prefix) {
  return `${prefix}-${randomUUID()}`
}

export function loopbackHTTPProxy(environment = process.env) {
  const candidate = environment.HTTPS_PROXY ??
    environment.https_proxy ??
    environment.HTTP_PROXY ??
    environment.http_proxy ??
    ''
  if (!candidate) return undefined

  let parsed
  try {
    parsed = new URL(candidate)
  } catch {
    return undefined
  }
  if (
    parsed.protocol !== 'http:' ||
    !['127.0.0.1', 'localhost'].includes(parsed.hostname) ||
    !parsed.port ||
    parsed.username ||
    parsed.password ||
    parsed.pathname !== '/' ||
    parsed.search ||
    parsed.hash
  ) {
    return undefined
  }
  return { server: parsed.origin }
}

export function validateWindowState(state, expected) {
  invariant(state?.schema_version === 1, 'managed OAuth window schema mismatch')
  invariant(state?.phase === 'open', 'managed OAuth window is not open')
  invariant(state?.deployment_id === expected.deploymentID, 'window deployment ID mismatch')
  invariant(state?.deployment_url === expected.deploymentURL, 'window deployment URL mismatch')
  invariant(state?.deployment_commit === expected.deploymentCommit, 'window commit mismatch')
  invariant(/^[0-9a-f]{32}$/.test(state?.window_id ?? ''), 'window ID is invalid')
  invariant(
    typeof state?.opened_at === 'string' && state.opened_at.length > 0,
    'window opened_at is missing',
  )
  return state
}

export function validateReleaseProvenance(observed, expected) {
  invariant(observed?.status === 'VERIFIED', 'release provenance is not VERIFIED')
  invariant(
    observed?.deploymentID === expected.deploymentID,
    'release provenance deployment ID mismatch',
  )
  invariant(
    observed?.deploymentURL === expected.deploymentURL,
    'release provenance deployment URL mismatch',
  )
  invariant(
    observed?.releaseCommit === expected.releaseCommit,
    'release provenance commit mismatch',
  )
  return observed
}

export function oauthCallbackError(bodyText) {
  if (typeof bodyText !== 'string' || bodyText.length === 0) return undefined
  try {
    const payload = JSON.parse(bodyText)
    if (
      typeof payload?.code === 'string' &&
      typeof payload?.message === 'string' &&
      payload.code.includes('oauth')
    ) {
      return {
        code: payload.code,
        message: payload.message,
      }
    }
  } catch {
    return undefined
  }
  return undefined
}

export function isExpectedVercelToolbarCSPError(message) {
  return typeof message === 'string' &&
    message.includes(
      "Loading the script 'https://vercel.live/_next-live/feedback/feedback.js'",
    ) &&
    message.includes('Content Security Policy directive') &&
    message.includes("script-src 'self'") &&
    message.includes('The action has been blocked')
}

export function isOAuthRedirectNavigationAbort(error) {
  return error instanceof Error && error.message.includes('net::ERR_ABORTED')
}

export async function requirePrivateRegularFile(file) {
  const stat = await lstat(file)
  invariant(stat.isFile() && !stat.isSymbolicLink(), `unsafe private file: ${file}`)
  const mode = stat.mode & 0o777
  invariant(mode === 0o600 || mode === 0o400, `private file mode must be 0600/0400: ${file}`)
}

export async function writePrivateJSON(file, value) {
  const directory = path.dirname(file)
  await mkdir(directory, { recursive: true, mode: 0o700 })
  await chmod(directory, 0o700)
  const temporary = path.join(
    directory,
    `.${path.basename(file)}.${process.pid}.${randomUUID()}.tmp`,
  )
  await writeFile(temporary, `${JSON.stringify(value, null, 2)}\n`, {
    mode: 0o600,
    flag: 'wx',
  })
  await chmod(temporary, 0o600)
  await rename(temporary, file)
  await chmod(file, 0o600)
}

export function balanceAvailable(payload, asset) {
  const balance = payload?.balances?.find((item) => item?.asset === asset)
  invariant(balance && typeof balance.available === 'string', `missing ${asset} balance`)
  return balance.available
}
