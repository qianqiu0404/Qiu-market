import { createHash, createHmac, randomBytes, randomUUID } from 'node:crypto'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { getCache, waitUntil } from '@vercel/functions'
import {
  PublicReadCache,
  RuntimePublicReadCache,
  type BackendMarketContract,
  type PublicReadCacheEntry,
  type PublicReadCacheLookup,
  agePublicReadBody,
  isPublicMarketRead,
  publicReadCachePayload,
} from '../server/public-read-cache.js'

const MAX_BODY_BYTES = 1 << 20
const MAX_PROXY_PATH_BYTES = 1_024
const TOTAL_UPSTREAM_TIMEOUT_MS = 8_000
const FIRST_READ_ATTEMPT_TIMEOUT_MS = 3_500
const RUNTIME_CACHE_TIMEOUT_MS = 250
const PUBLIC_CACHE_CONTROL =
	'public, max-age=0, s-maxage=15, stale-while-revalidate=240, stale-if-error=240'
const RELEASE_COMMIT_PATTERN = /^[0-9a-f]{40}$/i
const BACKEND_DATA_MODE = 'live'
const BACKEND_PROVIDER_POLICY = 'restricted-no-bypass.v1'
const BACKEND_CONTRACT_SCHEMA = 'qiu.market-read-contract.v1'
const BACKEND_SNAPSHOT_SCHEMA = 'qiu.market-snapshot.v1'
const EDGE_CONTRACT_SCHEMA = 'qiu.market-edge-contract.v1'
const DEPLOYMENT_ID_PATTERN = /^dpl_[A-Za-z0-9]+$/
const RETRYABLE_GET_PATHS = [
  /^\/api\/v1\/trading\/auth\/capabilities$/,
  /^\/api\/v1\/trading\/session$/,
  /^\/api\/v1\/trading\/markets\/[^/]+\/(?:orderbook|trades|status)$/,
  /^\/api\/v1\/trading\/orders(?:\/[^/]+)?$/,
  /^\/api\/v1\/trading\/trades$/,
  /^\/api\/v1\/trading\/balances$/,
]

const cacheGlobal = globalThis as typeof globalThis & {
  qiuMarketPublicReadCache?: PublicReadCache
}
const publicReadCache =
  cacheGlobal.qiuMarketPublicReadCache ?? new PublicReadCache()
cacheGlobal.qiuMarketPublicReadCache = publicReadCache
const runtimePublicReadCache = new RuntimePublicReadCache(
  getCache({ namespace: 'qiu-market-public-read-v1' }),
)

interface QiuProxyRequest extends IncomingMessage {
  body?: unknown
  query: Record<string, string | string[] | undefined>
}

interface QiuProxyResponse extends ServerResponse {
  status(code: number): QiuProxyResponse
  json(value: unknown): void
  send(value: Buffer): void
}

export interface ReleaseProvenance {
  status: 'VERIFIED' | 'UNCONFIGURED'
  commit?: string
  deploymentID?: string
  deploymentURL?: string
}

function normalizedDeploymentURL(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed || /[\r\n]/.test(trimmed)) return undefined
  try {
    const url = new URL(
      /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)
        ? trimmed
        : `https://${trimmed}`,
    )
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    ) {
      return undefined
    }
    url.pathname = url.pathname.replace(/\/+$/, '')
    return url.toString().replace(/\/$/, '')
  } catch {
    return undefined
  }
}

export function releaseProvenance(): ReleaseProvenance {
  const commit = process.env.QIU_MARKET_RELEASE_COMMIT?.trim().toLowerCase()
  const deploymentID = (
    process.env.QIU_MARKET_DEPLOYMENT_ID ??
    process.env.VERCEL_DEPLOYMENT_ID
  )?.trim()
  const deploymentURL = normalizedDeploymentURL(
    process.env.QIU_MARKET_DEPLOYMENT_URL ?? process.env.VERCEL_URL,
  )
  if (
    !commit ||
    !RELEASE_COMMIT_PATTERN.test(commit) ||
    !deploymentID ||
    !DEPLOYMENT_ID_PATTERN.test(deploymentID) ||
    !deploymentURL
  ) {
    return { status: 'UNCONFIGURED' }
  }
  return {
    status: 'VERIFIED',
    commit,
    deploymentID,
    deploymentURL,
  }
}

function setReleaseProvenanceHeaders(response: QiuProxyResponse): void {
  const provenance = releaseProvenance()
  response.setHeader('X-Qiu-Market-Provenance', provenance.status)
  if (provenance.commit) {
    response.setHeader('X-Qiu-Market-Release-Commit', provenance.commit)
  }
  if (provenance.deploymentID) {
    response.setHeader('X-Qiu-Market-Deployment-ID', provenance.deploymentID)
  }
  if (provenance.deploymentURL) {
    response.setHeader('X-Qiu-Market-Deployment-URL', provenance.deploymentURL)
  }
}

function requiredEnvironment(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) throw new Error(`${name} is required`)
  return value
}

function requestBody(request: QiuProxyRequest): Buffer {
  if (request.method === 'GET' || request.method === 'HEAD' || request.body == null) {
    return Buffer.alloc(0)
  }
  if (Buffer.isBuffer(request.body)) return request.body
  if (typeof request.body === 'string') return Buffer.from(request.body)
  return Buffer.from(JSON.stringify(request.body))
}

function firstHeader(
  value: string | string[] | undefined,
): string | undefined {
  return Array.isArray(value) ? value[0] : value
}

function safeProxyPath(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const normalized = value.replace(/^\/+/, '')
  if (
    normalized.length === 0 ||
    Buffer.byteLength(normalized) > MAX_PROXY_PATH_BYTES ||
    /[\u0000-\u001f\u007f\\?#]/.test(normalized)
  ) {
    return undefined
  }
  let decoded: string
  try {
    decoded = decodeURIComponent(normalized)
  } catch {
    return undefined
  }
  if (
    /[\u0000-\u001f\u007f\\?#]/.test(decoded) ||
    decoded.split('/').some((part) => part === '.' || part === '..')
  ) {
    return undefined
  }
  return normalized
}

function copyResponseHeader(
  response: QiuProxyResponse,
  upstream: Response,
  name: string,
): void {
  const value = upstream.headers.get(name)
  if (value) response.setHeader(name, value)
}

function sendCachedResponse(
  response: QiuProxyResponse,
  lookup: PublicReadCacheLookup,
  startedAt: number,
  pathname: string,
): void {
  response.setHeader('Cache-Control', PUBLIC_CACHE_CONTROL)
  response.setHeader('Age', lookup.ageSeconds.toString())
  response.setHeader('X-Qiu-Market-Cache', lookup.state.toUpperCase())
  response.setHeader(
    'Server-Timing',
    `qiu_cache;desc=${lookup.state};dur=${Math.max(0, Date.now() - startedAt)}`,
  )
  if (lookup.entry.contentType) {
    response.setHeader('Content-Type', lookup.entry.contentType)
  }
  if (lookup.entry.vary) response.setHeader('Vary', lookup.entry.vary)
  setBackendContractHeaders(response, lookup.entry.contract)
  if (lookup.state === 'stale') {
    response.setHeader('Warning', '110 - "Response is stale"')
  }
  response.status(lookup.entry.status).send(
    agePublicReadBody(
      pathname,
      lookup.entry.body,
      lookup.ageSeconds,
    ),
  )
}

async function bestEffortCacheOperation<T>(
  operation: Promise<T>,
): Promise<T | undefined> {
  let cancelTimeout: (() => void) | undefined
  try {
    return await Promise.race([
      operation.catch(() => undefined),
      new Promise<undefined>((resolve) => {
        const timeout = setTimeout(resolve, RUNTIME_CACHE_TIMEOUT_MS)
        cancelTimeout = () => clearTimeout(timeout)
      }),
    ])
  } finally {
    cancelTimeout?.()
  }
}

interface UpstreamFetchOptions {
  url: URL
  method: string
  headers: Headers
  body: Buffer
  digest: string
  secret: string
  retryable: boolean
  deadline: number
}

interface UpstreamFetchResult {
  response: Response
  body: Buffer
  requestNonce: string
}

export function publicProxyCanonical(
  timestamp: string,
  nonce: string,
  method: string,
  url: URL,
  digest: string,
): string {
  return [
    timestamp,
    nonce,
    method.toUpperCase(),
    url.pathname + url.search,
    digest,
  ].join('\n')
}

async function fetchUpstream({
  url,
  method,
  headers: forwardedHeaders,
  body,
  digest,
  secret,
  retryable,
  deadline,
}: UpstreamFetchOptions): Promise<UpstreamFetchResult> {
  let lastError: unknown
  const attempts = retryable ? 2 : 1
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const remaining = deadline - Date.now()
    if (remaining <= 0) break
    const timeoutMs = attempt === 0 && retryable
      ? Math.min(FIRST_READ_ATTEMPT_TIMEOUT_MS, remaining)
      : remaining
    const timestamp = Math.floor(Date.now() / 1000).toString()
    const nonce = randomBytes(16).toString('hex')
    const canonical = publicProxyCanonical(
      timestamp,
      nonce,
      method,
      url,
      digest,
    )
    const signature = createHmac('sha256', secret).update(canonical).digest('hex')
    const headers = new Headers(forwardedHeaders)
    headers.set('X-Qiu-Market-Timestamp', timestamp)
    headers.set('X-Qiu-Market-Nonce', nonce)
    headers.set('X-Qiu-Market-Content-SHA256', digest)
    headers.set('X-Qiu-Market-Signature', signature)
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), timeoutMs)
    try {
      const response = await fetch(url, {
        method,
        headers,
        body: method === 'GET' || method === 'HEAD' ? undefined : body,
        redirect: 'manual',
        signal: controller.signal,
      })
      const responseBody = Buffer.from(await response.arrayBuffer())
      if (
        retryable &&
        attempt === 0 &&
        [502, 503, 504].includes(response.status)
      ) {
        lastError = new Error(`retryable upstream HTTP ${response.status}`)
        continue
      }
      return { response, body: responseBody, requestNonce: nonce }
    } catch (error) {
      lastError = error
    } finally {
      clearTimeout(timeout)
    }
  }
  throw lastError ?? new DOMException('upstream deadline exceeded', 'AbortError')
}

function responseCookies(response: Response): string[] {
  const getSetCookie = (
    response.headers as Headers & { getSetCookie?: () => string[] }
  ).getSetCookie
  return getSetCookie?.call(response.headers) ?? []
}

function publicCacheEntry(
  response: Response,
  body: Buffer,
  cookies: string[],
  contract: BackendMarketContract,
): PublicReadCacheEntry | undefined {
  if (
    response.status !== 200 ||
    cookies.length > 0 ||
    !response.headers.get('content-type')?.includes('application/json')
  ) {
    return undefined
  }
  return {
    status: response.status,
    body,
    contentType: response.headers.get('content-type') ?? undefined,
    vary: response.headers.get('vary') ?? undefined,
    storedAt: Date.now(),
    contract,
  }
}

class BackendContractError extends Error {
  constructor(readonly reason: string) {
    super(`Qiu Market backend contract mismatch: ${reason}`)
    this.name = 'BackendContractError'
  }
}

function expectedBackendContract(): BackendMarketContract {
  const provenance = releaseProvenance()
  if (provenance.status !== 'VERIFIED' || !provenance.commit) {
    throw new BackendContractError('expected_release_unconfigured')
  }
  return {
    releaseCommit: provenance.commit,
    dataMode: BACKEND_DATA_MODE,
    providerPolicy: BACKEND_PROVIDER_POLICY,
    contractSchema: BACKEND_CONTRACT_SCHEMA,
    snapshotSchema: BACKEND_SNAPSHOT_SCHEMA,
		edgeReleaseCommit: provenance.commit,
		edgeDataMode: BACKEND_DATA_MODE,
		edgeContractSchema: EDGE_CONTRACT_SCHEMA,
  }
}

function contractFingerprint(contract: BackendMarketContract): string {
  return [
    contract.releaseCommit,
    contract.dataMode,
    contract.providerPolicy,
    contract.contractSchema,
    contract.snapshotSchema,
		contract.edgeReleaseCommit,
		contract.edgeDataMode,
		contract.edgeContractSchema,
  ].join('|')
}

function backendContract(
  upstream: Response,
  requestNonce: string,
  expected: BackendMarketContract,
): BackendMarketContract {
  const received: BackendMarketContract = {
    releaseCommit: upstream.headers.get('x-qiu-market-backend-release-commit')?.trim().toLowerCase() ?? '',
    dataMode: upstream.headers.get('x-qiu-market-data-mode')?.trim() ?? '',
    providerPolicy: upstream.headers.get('x-qiu-market-provider-policy')?.trim() ?? '',
    contractSchema: upstream.headers.get('x-qiu-market-contract-schema')?.trim() ?? '',
    snapshotSchema: upstream.headers.get('x-qiu-market-snapshot-schema')?.trim() ?? '',
		edgeReleaseCommit: upstream.headers.get('x-qiu-market-edge-release-commit')?.trim().toLowerCase() ?? '',
		edgeDataMode: upstream.headers.get('x-qiu-market-edge-data-mode')?.trim() ?? '',
		edgeContractSchema: upstream.headers.get('x-qiu-market-edge-contract-schema')?.trim() ?? '',
  }
	const legacyDataMode = upstream.headers.get('x-qiu-data-mode')?.trim() ?? ''
	if (legacyDataMode !== '' && legacyDataMode !== BACKEND_DATA_MODE) {
		throw new BackendContractError('legacy_data_mode_mismatch')
	}
  const echoedNonce = upstream.headers.get('x-qiu-market-backend-request-nonce')?.trim() ?? ''
  if (echoedNonce !== requestNonce) throw new BackendContractError('request_nonce_mismatch')
  for (const key of Object.keys(expected) as Array<keyof BackendMarketContract>) {
    if (received[key] !== expected[key]) {
      throw new BackendContractError(`${key}_mismatch`)
    }
  }
  return received
}

function setBackendContractHeaders(
  response: QiuProxyResponse,
  contract: BackendMarketContract,
): void {
  response.setHeader('X-Qiu-Market-Backend-Release-Commit', contract.releaseCommit)
  response.setHeader('X-Qiu-Market-Data-Mode', contract.dataMode)
  response.setHeader('X-Qiu-Market-Provider-Policy', contract.providerPolicy)
  response.setHeader('X-Qiu-Market-Contract-Schema', contract.contractSchema)
  response.setHeader('X-Qiu-Market-Snapshot-Schema', contract.snapshotSchema)
	response.setHeader('X-Qiu-Market-Edge-Release-Commit', contract.edgeReleaseCommit)
	response.setHeader('X-Qiu-Market-Edge-Data-Mode', contract.edgeDataMode)
	response.setHeader('X-Qiu-Market-Edge-Contract-Schema', contract.edgeContractSchema)
}

export function requiresBackendMarketContract(method: string, pathname: string): boolean {
	return isPublicMarketRead(method, pathname) ||
		(method === 'POST' && pathname === '/api/v2/get_market_price_ticks')
}

function requestedSnapshotID(body: Buffer): string {
	try {
		const parsed = JSON.parse(body.toString()) as { snapshot_id?: unknown }
		return typeof parsed.snapshot_id === 'string' ? parsed.snapshot_id.trim() : ''
	} catch {
		return ''
	}
}

function validateSnapshotEnvelope(
	pathname: string,
	requestBodyValue: Buffer,
	responseBody: Buffer,
	expected: BackendMarketContract,
): void {
	if (pathname !== '/api/v2/get_market_overview' && pathname !== '/api/v2/get_asset_dashboard') return
	let envelope: Record<string, unknown>
	try {
		envelope = JSON.parse(responseBody.toString()) as Record<string, unknown>
	} catch {
		throw new BackendContractError('snapshot_body_invalid')
	}
	const snapshotID = typeof envelope.snapshot_id === 'string' ? envelope.snapshot_id.trim() : ''
	if (!/^snp_[0-9a-f]{32}$/.test(snapshotID)) {
		throw new BackendContractError('snapshot_id_invalid')
	}
	if (Number(envelope.snapshot_as_of) <= 0) {
		throw new BackendContractError('snapshot_as_of_invalid')
	}
	if (envelope.snapshot_schema !== expected.snapshotSchema) {
		throw new BackendContractError('snapshot_schema_mismatch')
	}
	const requested = requestedSnapshotID(requestBodyValue)
	if (requested !== '' && requested !== snapshotID) {
		throw new BackendContractError('snapshot_id_mismatch')
	}
}

export function isRetryableUpstreamRequest(
  method: string,
  pathname: string,
): boolean {
  if (method !== 'GET') return false
  return RETRYABLE_GET_PATHS.some((pattern) => pattern.test(pathname))
}

export default async function handler(
  request: QiuProxyRequest,
  response: QiuProxyResponse,
): Promise<void> {
  response.setHeader('Cache-Control', 'no-store')
  setReleaseProvenanceHeaders(response)
  const requestID = firstHeader(request.headers['x-request-id']) ?? randomUUID()
  const startedAt = Date.now()
  response.setHeader('X-Request-ID', requestID)
  let staleLookup: PublicReadCacheLookup | undefined
  let cachedPathname = ''
  try {
    const backendOrigin = new URL(requiredEnvironment('S78_BACKEND_ORIGIN'))
    const insecureLoopback =
      process.env.S78_ALLOW_INSECURE_BACKEND === '1' &&
      backendOrigin.protocol === 'http:' &&
      (backendOrigin.hostname === '127.0.0.1' || backendOrigin.hostname === 'localhost')
    if (
      (backendOrigin.protocol !== 'https:' && !insecureLoopback) ||
      backendOrigin.username !== '' ||
      backendOrigin.password !== '' ||
      (backendOrigin.pathname !== '' && backendOrigin.pathname !== '/') ||
      backendOrigin.search !== '' ||
      backendOrigin.hash !== ''
    ) {
      throw new Error('S78_BACKEND_ORIGIN must be an exact HTTPS origin')
    }
    const secret = requiredEnvironment('S78_PROXY_HMAC_SECRET')
    const incomingURL = new URL(request.url ?? '/', 'https://qiu-market.invalid')
    const capturedPath = safeProxyPath(request.query.path)
    if (!capturedPath) {
      response.status(400).json({
        code: 'invalid_proxy_path',
        message: 'The Qiu Market proxy path is invalid.',
      })
      return
    }
    incomingURL.searchParams.delete('path')
    const originalPath = `/api/${capturedPath}`
    const upstreamURL = new URL(
      originalPath + (incomingURL.searchParams.size > 0 ? `?${incomingURL.searchParams}` : ''),
      backendOrigin,
    )
    if (
      upstreamURL.origin !== backendOrigin.origin ||
      !upstreamURL.pathname.startsWith('/api/')
    ) {
      response.status(400).json({
        code: 'invalid_proxy_path',
        message: 'The Qiu Market proxy path is invalid.',
      })
      return
    }
    cachedPathname = upstreamURL.pathname
    const body = requestBody(request)
    if (body.byteLength > MAX_BODY_BYTES) {
      response.status(413).json({
        code: 'request_too_large',
        message: 'Request body exceeds the Qiu Market proxy limit.',
      })
      return
    }
    const digest = createHash('sha256').update(body).digest('hex')
    const method = (request.method ?? 'GET').toUpperCase()
		const contractRequired = requiresBackendMarketContract(method, upstreamURL.pathname)
		const explicitSnapshotID = requestedSnapshotID(body)
		const cacheablePublicRead = isPublicMarketRead(method, upstreamURL.pathname) &&
			explicitSnapshotID === ''
		const expectedContract = contractRequired
      ? expectedBackendContract()
      : undefined
    const cacheDigest = cacheablePublicRead
      ? createHash('sha256').update(publicReadCachePayload(body)).digest('hex')
      : digest
    const cacheKey = `${expectedContract ? `${contractFingerprint(expectedContract)}\n` : ''}${method}\n${upstreamURL.pathname}${upstreamURL.search}\n${cacheDigest}`
    const cacheLookup = cacheablePublicRead
      ? publicReadCache.lookup(cacheKey)
      : undefined
    const sharedCacheLookup =
      cacheablePublicRead && !cacheLookup
        ? await bestEffortCacheOperation(
            runtimePublicReadCache.lookup(cacheKey),
          )
        : undefined
    if (sharedCacheLookup) {
      publicReadCache.put(cacheKey, sharedCacheLookup.entry)
    }
    const effectiveCacheLookup = cacheLookup ?? sharedCacheLookup
    if (
      effectiveCacheLookup &&
      expectedContract &&
      contractFingerprint(effectiveCacheLookup.entry.contract) !== contractFingerprint(expectedContract)
    ) {
      throw new BackendContractError('cached_contract_mismatch')
    }
		if (effectiveCacheLookup && expectedContract) {
			validateSnapshotEnvelope(
				upstreamURL.pathname,
				body,
				effectiveCacheLookup.entry.body,
				expectedContract,
			)
		}
    if (effectiveCacheLookup?.state === 'fresh') {
      sendCachedResponse(
        response,
        effectiveCacheLookup,
        startedAt,
        cachedPathname,
      )
      return
    }
    if (effectiveCacheLookup?.state === 'stale') {
      staleLookup = effectiveCacheLookup
    }
    const forwardedHeaders = new Headers()
    for (const name of [
      'accept',
      'content-type',
      'cookie',
      'origin',
      'referer',
      'user-agent',
      'x-csrf-token',
    ]) {
      const value = firstHeader(request.headers[name])
      if (value) forwardedHeaders.set(name, value)
    }
    forwardedHeaders.set('X-Forwarded-Host', firstHeader(request.headers.host) ?? 'qiu-market.vercel.app')
    forwardedHeaders.set('X-Forwarded-Proto', 'https')
    forwardedHeaders.set('X-Request-ID', requestID)

    const retryableRead = isRetryableUpstreamRequest(method, upstreamURL.pathname)
    const upstreamOptions = {
      url: upstreamURL,
      method,
      headers: forwardedHeaders,
      body,
      digest,
      secret,
      retryable: retryableRead,
    }
    const {
      response: upstream,
      body: upstreamBody,
      requestNonce: upstreamRequestNonce,
    } = await fetchUpstream({
      ...upstreamOptions,
      deadline: startedAt + TOTAL_UPSTREAM_TIMEOUT_MS,
    })

    const verifiedContract = expectedContract
      ? backendContract(upstream, upstreamRequestNonce, expectedContract)
      : undefined
		if (verifiedContract && upstream.status === 200) {
			validateSnapshotEnvelope(upstreamURL.pathname, body, upstreamBody, verifiedContract)
		}
    if (verifiedContract) setBackendContractHeaders(response, verifiedContract)
    copyResponseHeader(response, upstream, 'content-type')
    copyResponseHeader(response, upstream, 'location')
    copyResponseHeader(response, upstream, 'vary')
    const cookies = responseCookies(upstream)
    if (cookies.length > 0) response.setHeader('Set-Cookie', cookies)
    const cacheEntry = cacheablePublicRead
      ? publicCacheEntry(upstream, upstreamBody, cookies, verifiedContract as BackendMarketContract)
      : undefined
    if (cacheEntry) {
      publicReadCache.put(cacheKey, cacheEntry)
      waitUntil(
        runtimePublicReadCache
          .put(cacheKey, cacheEntry)
          .catch(() => false),
      )
      response.setHeader('Cache-Control', PUBLIC_CACHE_CONTROL)
      response.setHeader('Age', '0')
      response.setHeader('X-Qiu-Market-Cache', 'MISS')
    }
    response.setHeader(
      'Server-Timing',
      `qiu_backend;dur=${Math.max(0, Date.now() - startedAt)}`,
    )
    response.status(upstream.status).send(upstreamBody)
  } catch (error) {
    if (staleLookup && !(error instanceof BackendContractError)) {
      sendCachedResponse(response, staleLookup, startedAt, cachedPathname)
      return
    }
    if (error instanceof BackendContractError) {
      response.setHeader('Cache-Control', 'no-store')
      response.status(502).json({
        code: 'backend_contract_mismatch',
        message: 'Qiu Market backend identity or data contract did not match this release.',
        result: { reason: error.reason },
      })
      return
    }
    const timeout = error instanceof Error && error.name === 'AbortError'
    response.status(timeout ? 504 : 502).json({
      code: timeout ? 'backend_timeout' : 'backend_unavailable',
      message: timeout
        ? 'Qiu Market backend timed out.'
        : 'Qiu Market backend is unavailable.',
    })
  }
}

export const config = {
  maxDuration: 10,
}
