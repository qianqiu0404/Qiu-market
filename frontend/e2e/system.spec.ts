import { expect, test, type Page, type Route } from '@playwright/test'

type State = 'live' | 'cached' | 'degraded' | 'offline' | 'unknown'

interface EvidenceFixture {
  state: State
  last_success_at: number | null
  age_seconds: number | null
  reason: string
  source: string
}

function evidence(
  state: State,
  reason: string,
  source: string,
  now: number,
): EvidenceFixture {
  return {
    state,
    last_success_at: state === 'offline' || state === 'unknown' ? null : now,
    age_seconds: state === 'offline' || state === 'unknown' ? null : 0,
    reason,
    source,
  }
}

function metric(value: number) {
  return { available: true, value, reason: '' }
}

function writableRecoveryStatus(): Record<string, unknown> {
  return {
    schema_version: 1,
    market_id: 'BTC-USDT',
    epoch_id: '0123456789abcdef0123456789abcdef',
    phase: 'writable',
    runtime_sequence: '42',
    state_hash: 'a'.repeat(64),
    ledger_balanced: true,
    event_continuous: true,
    projection_caught_up: true,
    outbox_caught_up: true,
    transport_healthy: true,
    writes_enabled: true,
    continuity_uncertain: false,
    continuity_error: '',
    version: '6',
    started_at: '2026-08-05T00:00:00Z',
    updated_at: '2026-08-05T00:01:00Z',
  }
}

function healthySnapshot(now: number) {
  const components = {
    matching: evidence('live', 'Matching engine explicitly reports ready.', 'trading GetStatus', now),
    liquidity: evidence('live', 'Two-sided BTC-USDT liquidity is visible.', 'BTC-USDT public order book', now),
    transport: evidence('live', 'Trading status and order book reads both succeeded.', 'loopback trading REST over gRPC', now),
    market_data: evidence('live', 'CEX Spot reference data is current.', 'asset_price_index', now),
    outbox: evidence('live', 'Outbox publisher explicitly reports ready.', 'trading GetStatus outbox fields', now),
    database: evidence('live', 'PostgreSQL read probe succeeded.', 'system overview database_status', now),
    disk: evidence('live', 'Free disk is above the warning threshold.', 'filesystem statfs', now),
    retention: evidence('live', 'Retention succeeded within the expected daily window.', 'kline_retention_status', now),
  }
  return {
    schema_version: 'system-status.v1',
    formula_version: 'system-display.v1',
    source_mode: 'native',
    generated_at: now,
    overall: evidence(
      'live',
      'All required read-only probes have explicit current success evidence.',
      'system-display.v1',
      now,
    ),
    components,
    processes: [
      {
        key: 'crawler',
        label: 'Spot ingest supervisor',
        raw_status: 'Running',
        status: evidence('live', 'The latest heartbeat exists.', 'Redis heartbeat existence', now),
      },
      {
        key: 'database',
        label: 'PostgreSQL',
        raw_status: 'Connected',
        status: evidence('live', 'The dependency probe succeeded.', 'database read probe', now),
      },
    ],
    storage: {
      database_bytes: metric(8 * 2 ** 30),
      kline_table_bytes: metric(4 * 2 ** 30),
      kline_heap_bytes: metric(3 * 2 ** 30),
      kline_index_bytes: metric(1 * 2 ** 30),
      kline_estimated_rows: metric(1_250_000),
      disk_free_bytes: metric(40 * 2 ** 30),
      disk_state: 'healthy',
      warning_below_bytes: 25 * 2 ** 30,
      critical_below_bytes: 15 * 2 ** 30,
      retention_last_started_at: metric(now - 3_660_000),
      retention_last_success_at: metric(now - 3_600_000),
      retention_last_error: '',
      retention_deleted_rows: {
        '1m': metric(0),
        '15m': metric(12),
        '1h': metric(2),
      },
      kline_intervals: ['1m', '15m', '1h', '1d'].map((interval) => ({
        interval,
        oldest_at: metric(now - 7 * 24 * 60 * 60_000),
        newest_at: metric(now - 60_000),
      })),
    },
    price_sources: [
      {
        key: 'route_price',
        label: 'Route price',
        status: evidence('live', 'DEX route summaries are current.', 'route summaries', now),
        source: 'Uniswap and PancakeSwap venue route summaries',
        meaning: 'Venue-specific indicative route quotes at the reported notional.',
        boundary: 'Never substituted for the CEX Spot reference display price.',
      },
      {
        key: 'reference_display_price',
        label: 'Reference display price',
        status: components.market_data,
        source: 'asset_price_index built from fresh CEX Spot contributors',
        meaning: 'Read-only composite reference used for display and the virtual demo-maker.',
        boundary: 'Not an executable route price and never filled from DEX or mock data.',
      },
    ],
    provider_statuses: [],
  }
}

async function fulfillJSON(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}

async function installNativeStatus(
  page: Page,
  snapshot: ReturnType<typeof healthySnapshot>,
  recoveryStatus?: Record<string, unknown>,
  recoveryGateEnabled = true,
) {
  const effectiveRecoveryStatus = recoveryStatus ?? writableRecoveryStatus()
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/get_system_status') {
      await fulfillJSON(route, 200, { code: 2000, result: snapshot })
      return
    }
    if (path === '/api/v1/trading/recovery/status') {
      await fulfillJSON(
        route,
        recoveryGateEnabled ? 200 : 404,
        recoveryGateEnabled
          ? effectiveRecoveryStatus
          : { code: 'not_found', message: 'recovery gate not enabled' },
      )
      return
    }
    if (path === '/api/v1/trading/auth/capabilities') {
      await fulfillJSON(route, 200, {
        github_oauth_enabled: true,
        local_login_enabled: false,
        recovery_gate_enabled: recoveryGateEnabled,
      })
      return
    }
    if (path === '/api/v1/get_system_overview') {
      await fulfillJSON(route, 200, {
        code: 2000,
        result: {
          crawler_status: 'running',
          redis_status: 'connected',
          database_status: 'connected',
          worker_status: 'running',
          api_status: 'healthy',
          provider_statuses: [],
        },
      })
      return
    }
    await fulfillJSON(route, 404, { code: 4000, message: 'not found' })
  })
}

test('renders a healthy evidence contract with separate price-source columns', async ({ page }) => {
  const now = Date.now()
  await installNativeStatus(page, healthySnapshot(now))

  await page.goto('/system')

  const summary = page.locator('[data-state="live"]')
  await expect(summary).toContainText('LIVE')
  await expect(summary).toContainText('Native system-status contract')
  await expect(page.locator('[data-price-source="route_price"]')).toContainText('Route price')
  await expect(page.locator('[data-price-source="reference_display_price"]')).toContainText(
    'Reference display price',
  )
  await expect(page.getByText('DB 8.0 GB')).toBeVisible()
  await expect(page.getByText(/1m 0 ·/)).toBeVisible()
  await expect(page.getByText('1m candles')).toBeVisible()
})

test('shows recovery admission separately from the eight-probe formula', async ({ page }) => {
  const snapshot = healthySnapshot(Date.now())
  await installNativeStatus(page, snapshot, {
    schema_version: 1,
    market_id: 'BTC-USDT',
    epoch_id: '0123456789abcdef0123456789abcdef',
    phase: 'read_only',
    runtime_sequence: '42',
    state_hash: 'a'.repeat(64),
    ledger_balanced: true,
    event_continuous: true,
    projection_caught_up: true,
    outbox_caught_up: true,
    transport_healthy: false,
    writes_enabled: false,
    continuity_uncertain: false,
    continuity_error: '',
    version: '5',
  })

  await page.goto('/system')
  await expect(page.locator('.status-summary')).toContainText('LIVE')
  await expect(page.getByTestId('system-recovery-admission')).toHaveAttribute(
    'data-recovery-mode',
    'blocked',
  )
  await expect(page.getByTestId('system-recovery-admission')).toContainText('Read only')
  await expect(page.getByTestId('system-recovery-server-flag')).toHaveText('Blocked')
  await expect(page.getByTestId('system-recovery-effective-admission')).toHaveText('Blocked')
  await expect(page.getByText(/not a ninth input/)).toBeVisible()

  await page.getByRole('button', { name: '中文' }).click()
  await expect(page.getByTestId('system-recovery-admission')).toContainText('只读')
  await expect(page.getByTestId('system-recovery-server-flag')).toHaveText('已禁止')
  await expect(page.getByTestId('system-recovery-effective-admission')).toHaveText('已禁止')
  await expect(page.getByText(/不会成为现有八探针总状态公式的第九项输入/)).toBeVisible()
})

test('legacy recovery capability hides unsupported proof and continuity fields', async ({ page }) => {
  await installNativeStatus(page, healthySnapshot(Date.now()), undefined, false)

  await page.goto('/system')
  await expect(page.getByTestId('system-recovery-admission')).toHaveAttribute(
    'data-recovery-mode',
    'not_enabled',
  )
  await expect(page.getByTestId('system-recovery-server-flag')).toHaveText('Not reported')
  await expect(page.getByTestId('system-recovery-effective-admission')).toHaveText('Legacy gate')
  await expect(page.getByText('Proofs passed', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Continuity', { exact: true })).toHaveCount(0)
  await expect(page.getByText(/trusted capability explicitly reports/)).toBeVisible()
})

const degradedScenarios = [
  {
    name: 'partial trading failure',
    component: 'transport',
    state: 'degraded' as State,
    reason: 'Only one trading read succeeded.',
    expectedLabel: 'DEGRADED',
  },
  {
    name: 'cached market data',
    component: 'market_data',
    state: 'cached' as State,
    reason: 'CEX Spot reference data is served from the retained last success.',
    expectedLabel: 'CACHED',
  },
  {
    name: 'database unavailable',
    component: 'database',
    state: 'offline' as State,
    reason: 'PostgreSQL read probe failed.',
    expectedLabel: 'DEGRADED',
  },
  {
    name: 'critical disk threshold',
    component: 'disk',
    state: 'degraded' as State,
    reason: 'Free disk is below 15 GB.',
    expectedLabel: 'DEGRADED',
  },
  {
    name: 'retention failure',
    component: 'retention',
    state: 'degraded' as State,
    reason: 'The latest retention run failed.',
    expectedLabel: 'DEGRADED',
  },
]

for (const scenario of degradedScenarios) {
  test(`renders ${scenario.name} without promoting it to LIVE`, async ({ page }) => {
    const now = Date.now()
    const snapshot = healthySnapshot(now)
    snapshot.components[scenario.component as keyof typeof snapshot.components] = evidence(
      scenario.state,
      scenario.reason,
      `${scenario.component} test source`,
      now,
    )
    snapshot.overall = evidence(
      scenario.expectedLabel === 'CACHED' ? 'cached' : 'degraded',
      scenario.expectedLabel === 'CACHED'
        ? 'Only market data is using a retained last success within five minutes.'
        : 'One or more required probes are stale, failed, or missing explicit evidence.',
      'system-display.v1',
      now,
    )
    if (scenario.component === 'disk') {
      snapshot.storage.disk_free_bytes = metric(10 * 2 ** 30)
      snapshot.storage.disk_state = 'critical'
    }
    if (scenario.component === 'retention') {
      snapshot.storage.retention_last_error = 'delete batch failed'
    }
    await installNativeStatus(page, snapshot)

    await page.goto('/system')

    await expect(page.locator('.status-summary')).toContainText(scenario.expectedLabel)
    const component = page.locator(`[data-component="${scenario.component}"]`)
    await expect(component).toContainText(scenario.reason)
    await expect(component).not.toContainText('LIVE')
  })
}

test('adapts an old backend but leaves missing storage and outbox unavailable', async ({ page }) => {
  const now = Date.now()
  const pageErrors: string[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  await page.route(/\/api\/v[12]\//, async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/v1/get_system_status') {
      await fulfillJSON(route, 404, { message: 'not found' })
      return
    }
    if (path === '/api/v1/get_system_overview') {
      await fulfillJSON(route, 200, {
        code: 2000,
        result: {
          crawler_status: 'running',
          redis_status: 'connected',
          database_status: 'connected',
          worker_status: 'running',
          api_status: 'healthy',
          provider_statuses: [],
        },
      })
      return
    }
    if (path === '/api/v2/get_market_overview') {
      const body = JSON.parse(request.postData() ?? '{}') as { venue?: string }
      await fulfillJSON(route, 200, {
        code: 2000,
        result: body.venue === 'all'
          ? { priced_asset_count: 2, index_updated_at: now }
          : { routable_asset_count: 1, index_updated_at: now },
      })
      return
    }
    if (path.endsWith('/status')) {
      await fulfillJSON(route, 200, { state: 'ready' })
      return
    }
    if (path.endsWith('/orderbook')) {
      await fulfillJSON(route, 200, {
        bids: [{ price: '64990' }],
        asks: [{ price: '65010' }],
      })
      return
    }
    await fulfillJSON(route, 404, { message: 'not found' })
  })

  await page.goto('/system')

  await expect.poll(() => pageErrors).toEqual([])
  await expect(page.locator('.status-summary')).toContainText('Legacy backend compatibility')
  await expect(page.locator('.status-summary')).toContainText('DEGRADED')
  await expect(page.locator('[data-component="outbox"]')).toContainText('UNKNOWN')
  await expect(page.locator('[data-component="disk"]')).toContainText('UNKNOWN')
  await expect(page.getByText(/DB Unavailable ·/)).toBeVisible()
  await expect(page.getByText(/1m Unavailable ·/)).toBeVisible()
})

test('keeps the System status page within 1180px and 768px viewports', async ({ page }) => {
  await installNativeStatus(page, healthySnapshot(Date.now()))
  await page.setViewportSize({ width: 1180, height: 900 })
  await page.goto('/system')

  for (const width of [1180, 768]) {
    await page.setViewportSize({ width, height: 900 })
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  }
})
