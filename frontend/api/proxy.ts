import { createHash, createHmac, randomUUID } from 'node:crypto'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { getCache, waitUntil } from '@vercel/functions'
import {
  PublicReadCache,
  RuntimePublicReadCache,
  type PublicReadCacheEntry,
  type PublicReadCacheLookup,
  agePublicReadBody,
  isPublicMarketRead,
  publicReadCachePayload,
} from '../server/public-read-cache.js'

const MAX_BODY_BYTES = 1 << 20
const TOTAL_UPSTREAM_TIMEOUT_MS = 8_000
const FIRST_READ_ATTEMPT_TIMEOUT_MS = 3_500
const RUNTIME_CACHE_TIMEOUT_MS = 250
const PUBLIC_REVALIDATION_CONCURRENCY = 2
const PUBLIC_CACHE_CONTROL =
  'public, max-age=0, s-maxage=15, stale-while-revalidate=300, stale-if-error=300'
const RELEASE_COMMIT_PATTERN = /^[0-9a-f]{40}$/i
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
    const canonical = [
      timestamp,
      method,
      url.pathname + url.search,
      digest,
    ].join('\n')
    const signature = createHmac('sha256', secret).update(canonical).digest('hex')
    const headers = new Headers(forwardedHeaders)
    headers.set('X-Qiu-Market-Timestamp', timestamp)
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
      return { response, body: responseBody }
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
  }
}

const publicRevalidations = new Map<string, Promise<void>>()
let activePublicRevalidations = 0

function schedulePublicRevalidation(
  cacheKey: string,
  operation: () => Promise<void>,
): void {
  if (
    publicRevalidations.has(cacheKey) ||
    activePublicRevalidations >= PUBLIC_REVALIDATION_CONCURRENCY
  ) {
    return
  }
  activePublicRevalidations += 1
  const task = operation()
    .catch(() => undefined)
    .finally(() => {
      activePublicRevalidations -= 1
      publicRevalidations.delete(cacheKey)
    })
  publicRevalidations.set(cacheKey, task)
  waitUntil(task)
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
    if (backendOrigin.protocol !== 'https:' && !insecureLoopback) {
      throw new Error('S78_BACKEND_ORIGIN must use HTTPS')
    }
    const secret = requiredEnvironment('S78_PROXY_HMAC_SECRET')
    const incomingURL = new URL(request.url ?? '/', 'https://qiu-market.invalid')
    const capturedPath = firstHeader(request.query.path)
    if (!capturedPath || capturedPath.split('/').some((part) => part === '.' || part === '..')) {
      response.status(400).json({
        code: 'invalid_proxy_path',
        message: 'The Qiu Market proxy path is invalid.',
      })
      return
    }
    incomingURL.searchParams.delete('path')
    const originalPath = `/api/${capturedPath.replace(/^\/+/, '')}`
    const upstreamURL = new URL(
      originalPath + (incomingURL.searchParams.size > 0 ? `?${incomingURL.searchParams}` : ''),
      backendOrigin,
    )
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
    const cacheablePublicRead = isPublicMarketRead(method, upstreamURL.pathname)
    const cacheDigest = cacheablePublicRead
      ? createHash('sha256').update(publicReadCachePayload(body)).digest('hex')
      : digest
    const cacheKey = `${method}\n${upstreamURL.pathname}${upstreamURL.search}\n${cacheDigest}`
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
    if (staleLookup && cacheablePublicRead) {
      schedulePublicRevalidation(cacheKey, async () => {
        const refreshed = await fetchUpstream({
          ...upstreamOptions,
          deadline: startedAt + TOTAL_UPSTREAM_TIMEOUT_MS,
        })
        const cookies = responseCookies(refreshed.response)
        const cacheEntry = publicCacheEntry(
          refreshed.response,
          refreshed.body,
          cookies,
        )
        if (!cacheEntry) return
        publicReadCache.put(cacheKey, cacheEntry)
        await runtimePublicReadCache.put(cacheKey, cacheEntry)
      })
      sendCachedResponse(response, staleLookup, startedAt, cachedPathname)
      return
    }
    const {
      response: upstream,
      body: upstreamBody,
    } = await fetchUpstream({
      ...upstreamOptions,
      deadline: startedAt + TOTAL_UPSTREAM_TIMEOUT_MS,
    })

    copyResponseHeader(response, upstream, 'content-type')
    copyResponseHeader(response, upstream, 'location')
    copyResponseHeader(response, upstream, 'vary')
    const cookies = responseCookies(upstream)
    if (cookies.length > 0) response.setHeader('Set-Cookie', cookies)
    const cacheEntry = cacheablePublicRead
      ? publicCacheEntry(upstream, upstreamBody, cookies)
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
    if (staleLookup) {
      sendCachedResponse(response, staleLookup, startedAt, cachedPathname)
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
