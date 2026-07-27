import { createHash, createHmac } from 'node:crypto'
import type { IncomingMessage, ServerResponse } from 'node:http'

const MAX_BODY_BYTES = 1 << 20
const UPSTREAM_TIMEOUT_MS = 11_000

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

export default async function handler(
  request: QiuProxyRequest,
  response: QiuProxyResponse,
): Promise<void> {
  response.setHeader('Cache-Control', 'no-store')
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
    const timestamp = Math.floor(Date.now() / 1000).toString()
    const digest = createHash('sha256').update(body).digest('hex')
    const method = (request.method ?? 'GET').toUpperCase()
    const canonical = [
      timestamp,
      method,
      upstreamURL.pathname + upstreamURL.search,
      digest,
    ].join('\n')
    const signature = createHmac('sha256', secret).update(canonical).digest('hex')
    const headers = new Headers()
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
      if (value) headers.set(name, value)
    }
    headers.set('X-Qiu-Market-Timestamp', timestamp)
    headers.set('X-Qiu-Market-Content-SHA256', digest)
    headers.set('X-Qiu-Market-Signature', signature)
    headers.set('X-Forwarded-Host', firstHeader(request.headers.host) ?? 'qiu-market.vercel.app')
    headers.set('X-Forwarded-Proto', 'https')

    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), UPSTREAM_TIMEOUT_MS)
    let upstream: Response
    try {
      upstream = await fetch(upstreamURL, {
        method,
        headers,
        body: method === 'GET' || method === 'HEAD' ? undefined : body,
        redirect: 'manual',
        signal: controller.signal,
      })
    } finally {
      clearTimeout(timeout)
    }

    copyResponseHeader(response, upstream, 'content-type')
    copyResponseHeader(response, upstream, 'location')
    copyResponseHeader(response, upstream, 'vary')
    const getSetCookie = (
      upstream.headers as Headers & { getSetCookie?: () => string[] }
    ).getSetCookie
    const cookies = getSetCookie?.call(upstream.headers) ?? []
    if (cookies.length > 0) response.setHeader('Set-Cookie', cookies)
    response.status(upstream.status).send(Buffer.from(await upstream.arrayBuffer()))
  } catch (error) {
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
  maxDuration: 15,
}
