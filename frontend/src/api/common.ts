/** Typed API error thrown on non-200 HTTP status or envelope code !== 2000. */
export class ApiError extends Error {
  readonly code?: number
  readonly status?: number

  constructor(message: string, opts: { code?: number; status?: number } = {}) {
    super(message)
    this.name = 'ApiError'
    this.code = opts.code
    this.status = opts.status
  }
}

interface Envelope<T> {
  code: number
  result: T
  total?: number
  message?: string
  [key: string]: unknown
}

export interface RequestOutcome<T> {
  result: T
  total?: number
  [key: string]: unknown
}

/**
 * POST envelope convention: every request carries
 * `consumer_token: 'frontend-dashboard'`; success is `{ code: 2000, result, total? }`.
 * Throws ApiError on network failure, non-200 HTTP status, or code !== 2000.
 */
export async function request<T>(
  path: string,
  data: Record<string, unknown> = {},
  options: { signal?: AbortSignal } = {},
): Promise<RequestOutcome<T>> {
  let res: Response
  try {
    res = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ consumer_token: 'frontend-dashboard', ...data }),
      signal: options.signal,
    })
  } catch (error) {
    if (error instanceof Error && error.name === 'AbortError') throw error
    throw new ApiError('Network error: the API is unreachable')
  }

  let json: Envelope<T> | null = null
  try {
    json = (await res.json()) as Envelope<T>
  } catch {
    json = null
  }

  if (!res.ok) {
    throw new ApiError(json?.message ?? `Request failed (HTTP ${res.status})`, {
      code: json?.code,
      status: res.status,
    })
  }
  if (!json || json.code !== 2000) {
    throw new ApiError(json?.message ?? 'Unexpected API response', {
      code: json?.code,
      status: res.status,
    })
  }
  // Preserve endpoint-specific read metadata (for example momentum window
  // boundaries) while keeping result/total as the common typed contract.
  return { ...json, result: json.result, total: json.total }
}

export async function requestGET<T>(
  path: string,
  options: { signal?: AbortSignal } = {},
): Promise<RequestOutcome<T>> {
  let res: Response
  try {
    res = await fetch(path, {
      method: 'GET', headers: { Accept: 'application/json' }, signal: options.signal,
    })
  } catch (error) {
    if (error instanceof Error && error.name === 'AbortError') throw error
    throw new ApiError('Network error: the API is unreachable')
  }
  let json: Envelope<T> | null = null
  try { json = (await res.json()) as Envelope<T> } catch { json = null }
  if (!res.ok) {
    throw new ApiError(json?.message ?? `Request failed (HTTP ${res.status})`, {
      code: json?.code, status: res.status,
    })
  }
  if (!json || json.code !== 2000) {
    throw new ApiError(json?.message ?? 'Unexpected API response', {
      code: json?.code, status: res.status,
    })
  }
  return { ...json, result: json.result, total: json.total }
}
