import { createHash, createHmac, randomUUID } from 'node:crypto'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { getCache } from '@vercel/functions'
import {
  PublicReadCache,
  RuntimePublicReadCache,
  type PublicReadCacheLookup,
  isPublicMarketRead,
  publicReadCachePayload,
} from '../server/public-read-cache.js'

const MAX_BODY_BYTES = 1 << 20
const TOTAL_UPSTREAM_TIMEOUT_MS = 8_000
const FIRST_READ_ATTEMPT_TIMEOUT_MS = 3_500
const RUNTIME_CACHE_TIMEOUT_MS = 250
const PUBLIC_CACHE_CONTROL =
  'public, max-age=0, s-maxage=15, stale-while-revalidate=300, stale-if-error=300'
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
  response.status(lookup.entry.status).send(Buffer.from(lookup.entry.body))
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

export function isRetryableUpstreamRequest(
  method: string,
  pathname: string,
): boolean {
  if (isPublicMarketRead(method, pathname)) return true
  if (method !== 'GET') return false
  return RETRYABLE_GET_PATHS.some((pattern) => pattern.test(pathname))
}

export default async function handler(
  request: QiuProxyRequest,
  response: QiuProxyResponse,
): Promise<void> {
  response.setHeader('Cache-Control', 'no-store')
  const requestID = firstHeader(request.headers['x-request-id']) ?? randomUUID()
  const startedAt = Date.now()
  response.setHeader('X-Request-ID', requestID)
  let staleLookup: PublicReadCacheLookup | undefined
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
      sendCachedResponse(response, effectiveCacheLookup, startedAt)
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
    const deadline = startedAt + TOTAL_UPSTREAM_TIMEOUT_MS
    let upstream: Response | undefined
    let upstreamBody: Buffer | undefined
    let lastError: unknown
    const attempts = retryableRead ? 2 : 1
    for (let attempt = 0; attempt < attempts; attempt += 1) {
      const remaining = deadline - Date.now()
      if (remaining <= 0) break
      const timeoutMs = attempt === 0 && retryableRead
        ? Math.min(FIRST_READ_ATTEMPT_TIMEOUT_MS, remaining)
        : remaining
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const canonical = [
        timestamp,
        method,
        upstreamURL.pathname + upstreamURL.search,
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
        const candidate = await fetch(upstreamURL, {
          method,
          headers,
          body: method === 'GET' || method === 'HEAD' ? undefined : body,
          redirect: 'manual',
          signal: controller.signal,
        })
        const candidateBody = Buffer.from(await candidate.arrayBuffer())
        if (
          retryableRead &&
          attempt === 0 &&
          [502, 503, 504].includes(candidate.status)
        ) {
          lastError = new Error(`retryable upstream HTTP ${candidate.status}`)
          continue
        }
        upstream = candidate
        upstreamBody = candidateBody
        break
      } catch (error) {
        lastError = error
      } finally {
        clearTimeout(timeout)
      }
    }
    if (!upstream || !upstreamBody) {
      throw lastError ?? new DOMException('upstream deadline exceeded', 'AbortError')
    }
    if (
      staleLookup &&
      [502, 503, 504].includes(upstream.status)
    ) {
      sendCachedResponse(response, staleLookup, startedAt)
      return
    }

    copyResponseHeader(response, upstream, 'content-type')
    copyResponseHeader(response, upstream, 'location')
    copyResponseHeader(response, upstream, 'vary')
    const getSetCookie = (
      upstream.headers as Headers & { getSetCookie?: () => string[] }
    ).getSetCookie
    const cookies = getSetCookie?.call(upstream.headers) ?? []
    if (cookies.length > 0) response.setHeader('Set-Cookie', cookies)
    if (
      cacheablePublicRead &&
      upstream.status === 200 &&
      cookies.length === 0 &&
      upstream.headers.get('content-type')?.includes('application/json')
    ) {
      const cacheEntry = {
        status: upstream.status,
        body: upstreamBody,
        contentType: upstream.headers.get('content-type') ?? undefined,
        vary: upstream.headers.get('vary') ?? undefined,
        storedAt: Date.now(),
      }
      publicReadCache.put(cacheKey, cacheEntry)
      await bestEffortCacheOperation(
        runtimePublicReadCache.put(cacheKey, cacheEntry),
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
      sendCachedResponse(response, staleLookup, startedAt)
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
