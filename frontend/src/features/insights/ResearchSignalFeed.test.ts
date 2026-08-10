import { createApp, nextTick, type App } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '../../i18n'
import type { ResearchFeedStatus, ResearchSignalEvent, ResearchSignalEventPage, ResearchSignalSummary } from '../../api/research'
import ResearchSignalFeed from './ResearchSignalFeed.vue'

const event: ResearchSignalEvent = {
  id: 'evt-1', schemaVersion: 'researchsignal/v1' as const, type: 'market_event' as const,
  title: '<img src=x onerror=alert(1)> Federal Reserve schedule',
  summary: '<script>window.hacked=true</script> plain research text',
  source: 'xiuqiu-site Market Radar', provider: 'xiuqiu-site' as const,
  sourceUrl: 'https://xiuqiu-site.vercel.app/market-radar/events/evt-1', assets: ['BTC'],
  eventTime: '2026-08-10T01:00:00Z', observedAt: null,
  receivedAt: '2026-08-10T01:00:02Z', publishedAt: '2026-08-10T00:55:00Z',
  freshness: 'fresh' as const, editorialPriority: 'P1' as const,
  watchFor: 'Watch volatility.', invalidation: null,
  qualityFlags: ['observed_time_missing'], contentHash: `sha256:${'a'.repeat(64)}`,
  executable: false as const, sourceKind: 'xiuqiu_automated_dynamic' as const,
}

function feed(status: ResearchFeedStatus, items = [event]): ResearchSignalEventPage {
  return {
    schemaVersion: 'researchsignals/v1', status, generatedAt: '2026-08-10T01:00:03Z',
    data: { items, nextCursor: null }, error: null,
  }
}

const summary: ResearchSignalSummary = {
  schemaVersion: 'researchsignals/v1', status: 'fresh', generatedAt: '2026-08-10T01:00:03Z',
  data: {
    latestEventAt: event.eventTime, freshnessMinutes: 1, isDelayed: false,
    eventCount24h: 3, p0Count24h: 0, p1Count24h: 1,
    sources: [{ source: 'xiuqiu-site', status: 'healthy', lastSuccessAt: event.receivedAt, message: null }],
  },
  error: null,
}

let app: App<Element> | null = null
let host: HTMLElement | null = null

async function mount(props: {
  feed: ResearchSignalEventPage | null
  summary?: ResearchSignalSummary | null
  loading?: boolean
  error?: string | null
}): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp(ResearchSignalFeed, {
    summary: props.summary ?? null,
    loading: props.loading ?? false,
    error: props.error ?? null,
    feed: props.feed,
  })
  app.mount(host)
  await nextTick()
  return host
}

afterEach(() => {
  app?.unmount()
  host?.remove()
  app = null
  host = null
  setLocale('en')
})

describe('ResearchSignalFeed', () => {
  it('renders provenance, all times, priority and nullable conditions as plain text', async () => {
    const view = await mount({ feed: feed('fresh'), summary })
    expect(view.textContent).toContain('<img src=x onerror=alert(1)>')
    expect(view.textContent).toContain('<script>window.hacked=true</script>')
    expect(view.querySelector('img')).toBeNull()
    expect(view.querySelector('script')).toBeNull()
    expect(view.textContent).toContain('xiuqiu-site Market Radar')
    expect(view.textContent).toContain('xiuqiu-site · xiuqiu_automated_dynamic')
    expect(view.textContent).toContain('Research priority (not trading advice) P1')
    expect(view.textContent).toContain('Watch volatility.')
    expect(view.textContent).toContain('Not supplied')
    expect(view.textContent).toContain('Research information · Not executable')
    expect(view.querySelector('a')?.getAttribute('href')).toBe('https://xiuqiu-site.vercel.app/market-radar/events/evt-1')
  })

  it.each(['empty', 'legacy', 'degraded', 'stale', 'partial', 'unconfigured'] as ResearchFeedStatus[])(
    'keeps the %s state explicit instead of presenting it as a successful empty feed',
    async (status) => {
      const view = await mount({ feed: feed(status, status === 'empty' ? [] : [event]) })
      expect(view.querySelector(`[data-testid="research-state-${status}"]`)).not.toBeNull()
      expect(view.querySelector('[data-testid="research-feed-status"]')?.textContent?.toLowerCase())
        .toContain(status)
    },
  )

  it('renders an explicit load failure with retry and no false empty copy', async () => {
    const view = await mount({ feed: null, error: 'upstream unavailable' })
    expect(view.textContent).toContain('Research request failed')
    expect(view.textContent).toContain('upstream unavailable')
    expect(view.textContent).not.toContain('No research events in this window')
    expect(view.querySelector('button')).not.toBeNull()
  })

  it('lets an unsafe summary state dominate a fresh or empty event page', async () => {
    const degradedSummary = {
      ...summary,
      status: 'degraded' as const,
      error: { code: 'upstream', message: 'partial outage', retryAfterSeconds: null },
    }
    const view = await mount({ feed: feed('empty', []), summary: degradedSummary })
    expect(view.querySelector('[data-testid="research-state-degraded"]')).not.toBeNull()
    expect(view.textContent).not.toContain('verified empty window')
  })

  it('marks a truncated first page as partial when a next cursor is present', async () => {
    const truncated = feed('fresh')
    truncated.data.nextCursor = 'opaque-next'
    const view = await mount({ feed: truncated })
    expect(view.querySelector('[data-testid="research-state-partial"]')).not.toBeNull()
  })

  it('does not turn an unsafe URL into a link even when a caller bypasses API parsing', async () => {
    const unsafe = { ...event, sourceUrl: 'javascript:alert(1)' }
    const view = await mount({ feed: feed('fresh', [unsafe]) })
    expect(view.querySelector('a')).toBeNull()
    expect(view.textContent).toContain('xiuqiu-site Market Radar')
  })

  it('reacts to the Chinese locale without hiding the non-executable boundary', async () => {
    setLocale('zh-CN')
    const view = await mount({ feed: feed('fresh') })
    expect(view.textContent).toContain('BTC 研究信息流')
    expect(view.textContent).toContain('研究信息 · 不可执行')
    expect(view.textContent).toContain('研究优先级（非交易建议） P1')
    expect(view.textContent).toContain('失效条件')
  })
})
