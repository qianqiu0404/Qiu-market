import { afterEach, describe, expect, it, vi } from 'vitest'
import { getDataQualitySummary } from './dataQuality'

const RULES = {
  binance_spot: { name: 'Binance Public', class: 'spot', use: 'market_context', caps: ['spot_ticker', 'ohlcv'] },
  coinglass_derivatives: { name: 'CoinGlass', class: 'derivatives', use: 'derivatives_context', caps: ['open_interest', 'liquidation'] },
  xiuqiu_research: { name: 'xiuqiu-site Market Radar', class: 'research', use: 'research_context', caps: ['research_summary', 'research_events'] },
} as const

function counter(numerator = 2, denominator = 2) {
  return { numerator, denominator, bps: denominator === 0 ? null : Math.round(numerator * 10_000 / denominator) }
}

function capability(name: string, patch: Record<string, unknown> = {}) {
  return {
    capability: name, maxAgeSeconds: 65, sampleCount: 2, validSampleCount: 2, minSamples: 2, successCount: 2,
    lastAttemptAt: '2026-08-10T05:59:59Z', lastSuccessAt: '2026-08-10T05:59:58Z', ageSeconds: 2,
    coverage: counter(), status: 'healthy', reasons: [], ...patch,
  }
}

function source(name: keyof typeof RULES, patch: Record<string, unknown> = {}) {
  const rule = RULES[name]
  return {
    source: name, sourceName: rule.name, class: rule.class,
    windowStart: '2026-08-10T05:55:00Z', windowEnd: '2026-08-10T06:00:00Z', windowSeconds: 300,
    sampleCount: 4, minSamples: 2, attemptCount: 4, successCount: 4,
    lastAttemptAt: '2026-08-10T05:59:59Z', lastSuccessAt: '2026-08-10T05:59:58Z', ageSeconds: 2,
    coverage: counter(), technicalScoreBps: 9500, grade: 'A', status: 'healthy', reasons: [],
    license: 'approved', publicEligible: true, tradeEligible: false, readOnlyUse: rule.use,
    capabilities: rule.caps.map((cap) => capability(cap)),
    dimensions: ['freshness', 'availability', 'completeness', 'schema', 'consistency', 'coverage'].map((metric) => ({
      metric, polarity: 'positive', ...counter(),
    })),
    errorCounts: { timeout: 0, upstream_5xx: 0, rate_limit: 0 }, cacheHitCount: 0, staleServeCount: 0,
    priorityCounts: name === 'xiuqiu_research' ? { p0: 1, p1: 2, p2: 1 } : { p0: 0, p1: 0, p2: 0 },
    gate: { status: 'healthy', healthyWindowStreak: 3, recoveryRequired: 3, reasons: [] },
    ...patch,
  }
}

function payload() {
  return {
    schemaVersion: 'data-quality/v1', status: 'insufficient', generatedAt: '2026-08-10T06:00:00Z',
    items: [
      source('binance_spot'),
      source('coinglass_derivatives', {
        windowStart: null, windowEnd: null, windowSeconds: null, sampleCount: 0, minSamples: 2,
        attemptCount: 0, successCount: 0, lastAttemptAt: null, lastSuccessAt: null, ageSeconds: null,
        coverage: counter(0, 0), technicalScoreBps: null, grade: null, status: 'insufficient',
        reasons: ['not_live'], license: 'unknown', publicEligible: false, dimensions: [], errorCounts: {},
        priorityCounts: { p0: 0, p1: 0, p2: 0 },
        gate: { status: 'insufficient', healthyWindowStreak: 0, recoveryRequired: 3, reasons: ['not_live'] },
        capabilities: RULES.coinglass_derivatives.caps.map((cap) => capability(cap, {
          sampleCount: 0, validSampleCount: 0, minSamples: 1, successCount: 0, lastAttemptAt: null, lastSuccessAt: null,
          ageSeconds: null, coverage: counter(0, 1), status: 'insufficient', reasons: ['not_live'],
        })),
      }),
      source('xiuqiu_research'),
    ],
    error: null,
  }
}

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => vi.unstubAllGlobals())

describe('data quality API', () => {
  it('uses one fixed read-only GET and preserves source/capability evidence separately', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toBe('/api/v1/data-quality/summary')
      expect(init).toMatchObject({ method: 'GET', credentials: 'same-origin' })
      return jsonResponse(payload())
    })
    vi.stubGlobal('fetch', fetchMock)
    const result = await getDataQualitySummary()
    expect(result.items).toHaveLength(3)
    expect(result.items.flatMap((item) => item.capabilities)).toHaveLength(6)
    expect(result.items[1]).toMatchObject({ grade: null, technicalScoreBps: null, tradeEligible: false })
    expect(result.items[0]).toMatchObject({ cacheHitCount: 0, staleServeCount: 0 })
  })

  it('accepts cache evidence outside the attempt denominator', async () => {
    const value = payload()
    value.items[0].sampleCount = 5
    value.items[0].cacheHitCount = 1
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    const result = await getDataQualitySummary()
    expect(result.items[0]).toMatchObject({ sampleCount: 5, attemptCount: 4, cacheHitCount: 1 })
  })

  it('keeps successful no-data fetches below the valid-sample minimum', async () => {
    const value = payload()
    value.items[0] = source('binance_spot', {
      technicalScoreBps: null, grade: null, status: 'insufficient', publicEligible: false,
      gate: { status: 'insufficient', healthyWindowStreak: 0, recoveryRequired: 3, reasons: ['min_samples'] },
      capabilities: [capability('spot_ticker', {
        validSampleCount: 0, coverage: counter(0, 2), status: 'insufficient', reasons: ['no_data'],
      }), capability('ohlcv')],
    })
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    const result = await getDataQualitySummary()
    expect(result.items[0].capabilities[0]).toMatchObject({ sampleCount: 2, successCount: 2, validSampleCount: 0, status: 'insufficient' })
  })

  it.each(['stale', 'future', 'conflict'])('accepts a below-minimum %s hard-fault quarantine', async (reason) => {
    const value = payload()
    value.status = 'quarantined'
    value.items[0] = source('binance_spot', {
      status: 'quarantined', publicEligible: false, reasons: [`hard_fault:${reason}`],
      gate: { status: 'quarantined', healthyWindowStreak: 0, recoveryRequired: 3, reasons: [`hard_fault:${reason}`] },
      capabilities: [capability('spot_ticker', {
        validSampleCount: 1, coverage: counter(1, 2), status: 'quarantined', reasons: ['hard_fault', reason],
      }), capability('ohlcv')],
    })
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    const result = await getDataQualitySummary()
    expect(result.items[0].capabilities[0]).toMatchObject({ validSampleCount: 1, status: 'quarantined' })
  })

  it.each(['healthy', 'degraded', 'recovering'])('rejects below-minimum %s even when a hard-fault reason is present', async (status) => {
    const value = payload()
    value.items[0].capabilities[0] = capability('spot_ticker', {
      validSampleCount: 1, coverage: counter(1, 2), status, reasons: ['hard_fault', 'stale'],
    })
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'invalid_response' })
  })

  it('rejects a below-minimum quarantine without an allowlisted hard fault', async () => {
    const value = payload()
    value.items[0].capabilities[0] = capability('spot_ticker', {
      validSampleCount: 1, coverage: counter(1, 2), status: 'quarantined', reasons: ['attempt_failures'],
    })
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'invalid_response' })
  })

  it.each([
    ['trading eligibility', (value: ReturnType<typeof payload>) => { value.items[0].tradeEligible = true }],
    ['canonical publisher', (value: ReturnType<typeof payload>) => { (value.items[0] as { sourceName: string }).sourceName = 'lookalike' }],
    ['license/public gate', (value: ReturnType<typeof payload>) => { value.items[0].license = 'restricted' }],
    ['attempt count', (value: ReturnType<typeof payload>) => { value.items[0].successCount = 5 }],
    ['score/grade', (value: ReturnType<typeof payload>) => { value.items[0].grade = 'C' }],
    ['window duration', (value: ReturnType<typeof payload>) => { value.items[0].windowSeconds = 301 }],
    ['missing stable metric', (value: ReturnType<typeof payload>) => { value.items[0].dimensions.pop() }],
    ['capability minimum', (value: ReturnType<typeof payload>) => { value.items[0].capabilities[0].validSampleCount = 1 }],
    ['invented empty ratio', (value: ReturnType<typeof payload>) => { value.items[0].coverage = { numerator: 0, denominator: 0, bps: 10_000 } }],
  ])('fails closed on %s drift', async (_name, mutate) => {
    const value = payload()
    mutate(value)
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'invalid_response' })
  })

  it('rejects non-JSON, incomplete sources, and an actually oversized stream', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { headers: { 'Content-Type': 'text/html' } })))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'invalid_content_type' })

    const value = payload()
    value.items.pop()
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'invalid_response' })

    vi.stubGlobal('fetch', vi.fn(async () => new Response(`{"padding":"${'x'.repeat(300_000)}"}`, {
      headers: { 'Content-Type': 'application/json' },
    })))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'response_too_large' })
  })

  it('rejects an overall badge that understates the worst source', async () => {
    const value = payload()
    value.status = 'insufficient'
    value.items[0] = source('binance_spot', {
      status: 'quarantined', technicalScoreBps: null, grade: null, publicEligible: false,
      gate: { status: 'quarantined', healthyWindowStreak: 0, recoveryRequired: 3, reasons: ['future'] },
    })
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(value)))
    await expect(getDataQualitySummary()).rejects.toMatchObject({ code: 'invalid_response' })
  })
})
