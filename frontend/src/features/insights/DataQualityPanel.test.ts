import { createApp, nextTick, type App } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import type { DataQualityItem, DataQualitySummary } from '../../api/dataQuality'
import DataQualityPanel from './DataQualityPanel.vue'

const dimensions: DataQualityItem['dimensions'] = [
  'freshness', 'availability', 'completeness', 'schema', 'consistency', 'coverage',
].map((metric) => ({ metric: metric as never, polarity: 'positive', numerator: 2, denominator: 2, bps: 10_000 }))

function item(source: DataQualityItem['source'], patch: Partial<DataQualityItem> = {}): DataQualityItem {
  const rules = {
    binance_spot: ['Binance Public', 'spot', 'market_context', ['spot_ticker', 'ohlcv']],
    coinglass_derivatives: ['CoinGlass', 'derivatives', 'derivatives_context', ['open_interest', 'liquidation']],
    xiuqiu_research: ['xiuqiu-site Market Radar', 'research', 'research_context', ['research_summary', 'research_events']],
  } as const
  const rule = rules[source]
  return {
    source, sourceName: rule[0], class: rule[1], windowStart: '2026-08-10T05:55:00Z',
    windowEnd: '2026-08-10T06:00:00Z', windowSeconds: 300, sampleCount: 4, minSamples: 2,
    attemptCount: 4, successCount: 3, lastAttemptAt: '2026-08-10T05:59:59Z',
    lastSuccessAt: '2026-08-10T05:59:58Z', ageSeconds: 2,
    coverage: { numerator: 2, denominator: 2, bps: 10_000 }, technicalScoreBps: 9_500,
    grade: 'A', status: 'healthy', reasons: [], license: 'approved', publicEligible: true,
    tradeEligible: false, readOnlyUse: rule[2], dimensions, errorCounts: { timeout: 1, upstream_5xx: 2 },
    cacheHitCount: 3, staleServeCount: 0,
    priorityCounts: source === 'xiuqiu_research' ? { p0: 1, p1: 2, p2: 1 } : { p0: 0, p1: 0, p2: 0 },
    gate: { status: 'healthy', healthyWindowStreak: 3, recoveryRequired: 3, reasons: [] },
    capabilities: rule[3].map((capability) => ({
      capability, maxAgeSeconds: 65, sampleCount: 2, validSampleCount: 2, minSamples: 2, successCount: 2,
      lastAttemptAt: '2026-08-10T05:59:59Z', lastSuccessAt: '2026-08-10T05:59:58Z', ageSeconds: 2,
      coverage: { numerator: 2, denominator: 2, bps: 10_000 }, status: 'healthy', reasons: [],
    })) as DataQualityItem['capabilities'],
    ...patch,
  }
}

const summary: DataQualitySummary = {
  schemaVersion: 'data-quality/v1', status: 'degraded', generatedAt: '2026-08-10T06:00:00Z', error: null,
  items: [item('binance_spot'), item('coinglass_derivatives', {
    sampleCount: 0, minSamples: 2, attemptCount: 0, successCount: 0, lastAttemptAt: null,
    lastSuccessAt: null, ageSeconds: null, coverage: { numerator: 0, denominator: 2, bps: 0 },
    technicalScoreBps: null, grade: null, status: 'insufficient', reasons: ['not_live'],
    license: 'unknown', publicEligible: false,
    gate: { status: 'insufficient', healthyWindowStreak: 0, recoveryRequired: 3, reasons: ['not_live'] },
  }), item('xiuqiu_research')],
}

let app: App<Element> | null = null
let host: HTMLElement | null = null

async function mount(value: DataQualitySummary | null, error: string | null = null) {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp(DataQualityPanel, { summary: value, loading: false, error })
  app.mount(host)
  await nextTick()
  return host
}

afterEach(() => { app?.unmount(); host?.remove(); app = null; host = null })

describe('DataQualityPanel', () => {
  it('shows three source scores and six independently labeled capabilities', async () => {
    const view = await mount(summary)
    expect(view.querySelectorAll('.quality-card')).toHaveLength(3)
    expect(view.querySelectorAll('.capability-card')).toHaveLength(6)
    expect(view.textContent).toContain('Binance Public')
    expect(view.textContent).toContain('CoinGlass')
    expect(view.textContent).toContain('xiuqiu-site Market Radar')
    expect(view.textContent).toContain('spot ticker')
    expect(view.textContent).toContain('open interest')
    expect(view.textContent).toContain('100.00%')
    expect(view.textContent).toContain('timeout=1')
    expect(view.textContent).toContain('upstream 5xx=2')
    expect(view.textContent).toContain('Cache hits=3')
    expect(view.textContent).toContain('Read-only quality · not trading advice')
    expect(view.textContent).toContain('Trade eligible')
    expect(view.querySelectorAll('.quality-boundary')).toHaveLength(3)
    expect(view.querySelector('[v-html]')).toBeNull()
  })

  it('keeps insufficient source score and unavailable timestamps explicit', async () => {
    const view = await mount(summary)
    expect(view.textContent).toContain('Insufficient evidence')
    expect(view.textContent).toContain('Not available')
    expect(view.textContent).toContain('not_live')
  })

  it('shows an explicit transport error with retry and no source cards', async () => {
    const view = await mount(null, 'quality monitor unreachable')
    expect(view.textContent).toContain('Quality report unavailable')
    expect(view.textContent).toContain('quality monitor unreachable')
    expect(view.querySelectorAll('.quality-card')).toHaveLength(0)
    expect(view.querySelector('button')).not.toBeNull()
  })
})
