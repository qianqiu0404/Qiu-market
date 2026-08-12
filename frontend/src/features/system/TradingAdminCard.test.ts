import { createApp, nextTick, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  TradingRequestError,
  tradingAPI,
  type FundingRequestResult,
} from '../../api/trading'
import { setLocale, systemTradingMessageKeys } from '../../i18n'
import {
  PENDING_TRADING_WRITE_STORAGE_KEY,
  type PendingTradingWrite,
} from '../../trading/pending-write'
import type { RecoveryAdmission } from '../../trading/recovery-admission'
import TradingAdminCard from './TradingAdminCard.vue'

const writable: RecoveryAdmission = {
  mode: 'writable',
  writesAllowed: true,
  reason: 'recovery_writable',
  completedProofs: 6,
  totalProofs: 6,
}

const blocked: RecoveryAdmission = {
  ...writable,
  mode: 'blocked',
  writesAllowed: false,
  reason: 'recovery_reconciling',
}

let mountedApp: App<Element> | null = null
let host: HTMLElement | null = null

async function settle(): Promise<void> {
  await Promise.resolve()
  await nextTick()
  await new Promise((resolve) => window.setTimeout(resolve, 0))
  await nextTick()
}

async function mountCard(admission: RecoveryAdmission | null = writable): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.append(host)
  mountedApp = createApp(TradingAdminCard, { admission })
  mountedApp.mount(host)
  await settle()
  return host
}

function setValue(element: HTMLInputElement | HTMLSelectElement, value: string): void {
  element.value = value
  element.dispatchEvent(new Event('input', { bubbles: true }))
  element.dispatchEvent(new Event('change', { bubbles: true }))
}

function starterResult(
  requestID: 'starter-v1-usdt' | 'starter-v1-btc',
  sequence: string,
): FundingRequestResult {
  const btc = requestID === 'starter-v1-btc'
  return {
    market_id: 'BTC-USDT',
    request_id: requestID,
    funding_event_id: `event:${sequence}:0`,
    sequence,
    asset: btc ? 'BTC' : 'USDT',
    amount: btc ? '0.1' : '10000',
    projection_result: 'applied',
    ledger_balanced: true,
    occurred_at: '2026-08-13T00:00:00Z',
  }
}

function pendingStarter(requestID: 'starter-v1-usdt' | 'starter-v1-btc'): PendingTradingWrite {
  const btc = requestID === 'starter-v1-btc'
  return {
    operation_id: `operation-${requestID}`,
    operation: 'fund',
    account_id: 'local:practice',
    request_id: requestID,
    state: 'unknown',
    created_at: 10,
    updated_at: 11,
    payload: {
      account_id: 'local:practice',
      asset: btc ? 'BTC' : 'USDT',
      amount: btc ? '0.1' : '10000',
    },
  }
}

beforeEach(() => {
  setLocale('en')
  vi.spyOn(tradingAPI, 'authCapabilities').mockResolvedValue({
    github_oauth_enabled: false,
    local_login_enabled: false,
    practice_mode_enabled: false,
    starter_funds_enabled: false,
    virtual_liquidity_enabled: false,
  })
  vi.spyOn(tradingAPI, 'status').mockResolvedValue({
    market_id: 'BTC-USDT',
    state: 'ready',
    sequence: '1',
    queue_depth: 0,
    recovery_count: '0',
    last_error: '',
  })
})

afterEach(() => {
  mountedApp?.unmount()
  host?.remove()
  mountedApp = null
  host = null
  window.localStorage.clear()
  window.sessionStorage.clear()
  vi.restoreAllMocks()
})

describe('System virtual funding control', () => {
  it('keeps every frozen System trading key in English and Chinese', () => {
    expect(systemTradingMessageKeys('zh-CN')).toEqual(systemTradingMessageKeys('en'))
    expect(systemTradingMessageKeys('en').length).toBeGreaterThan(20)
  })

  it('applies the two fixed starter events query-first and never funds them twice', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'local:practice', github_login: 'practice', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    vi.mocked(tradingAPI.authCapabilities).mockResolvedValue({
      github_oauth_enabled: false,
      local_login_enabled: true,
      recovery_gate_enabled: true,
      practice_mode_enabled: true,
      starter_funds_enabled: true,
      virtual_liquidity_enabled: true,
    })
    const applied = new Map<string, FundingRequestResult>()
    const trace: string[] = []
    vi.spyOn(tradingAPI, 'fundingRequest').mockImplementation(async (id) => {
      trace.push(`query:${id}`)
      const result = applied.get(id)
      if (result) return result
      throw new TradingRequestError(
        'not found', 'funding_request_not_found', 404, false,
      )
    })
    const fund = vi.spyOn(tradingAPI, 'fund').mockImplementation(async (id, asset, amount) => {
      trace.push(`fund:${id}`)
      const result = starterResult(id as 'starter-v1-usdt' | 'starter-v1-btc',
        id === 'starter-v1-usdt' ? '10' : '11')
      expect(result.asset).toBe(asset)
      expect(result.amount).toBe(amount)
      applied.set(id, result)
      return {}
    })
    const view = await mountCard()
    await settle()

    view.querySelector<HTMLButtonElement>('[data-testid="starter-funds-submit"]')!.click()
    await vi.waitFor(() => expect(fund).toHaveBeenCalledTimes(2))
    await settle()

    expect(fund.mock.calls).toEqual([
      ['starter-v1-usdt', 'USDT', '10000', 'local:practice'],
      ['starter-v1-btc', 'BTC', '0.1', 'local:practice'],
    ])
    expect(trace.indexOf('query:starter-v1-usdt'))
      .toBeLessThan(trace.indexOf('fund:starter-v1-usdt'))
    expect(trace.indexOf('query:starter-v1-btc'))
      .toBeLessThan(trace.indexOf('fund:starter-v1-btc'))
    expect(view.textContent).toContain('Both starter funding events are applied and balanced.')
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()

    view.querySelector<HTMLButtonElement>('[data-testid="starter-funds-submit"]')!.click()
    await settle()
    expect(fund).toHaveBeenCalledTimes(2)
  })

  it('queries an unknown starter result before one exact-ID replay', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'local:practice', github_login: 'practice', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    vi.mocked(tradingAPI.authCapabilities).mockResolvedValue({
      github_oauth_enabled: false,
      local_login_enabled: true,
      practice_mode_enabled: true,
      starter_funds_enabled: true,
      virtual_liquidity_enabled: true,
    })
    const applied = new Map<string, FundingRequestResult>()
    vi.spyOn(tradingAPI, 'fundingRequest').mockImplementation(async (id) => {
      const result = applied.get(id)
      if (result) return result
      throw new TradingRequestError(
        'not found', 'funding_request_not_found', 404, false,
      )
    })
    let usdtAttempts = 0
    const fund = vi.spyOn(tradingAPI, 'fund').mockImplementation(async (id) => {
      if (id === 'starter-v1-usdt' && usdtAttempts++ === 0) {
        throw new TradingRequestError('lost response', 'network_error', 0, true)
      }
      applied.set(id, starterResult(
        id as 'starter-v1-usdt' | 'starter-v1-btc',
        id === 'starter-v1-usdt' ? '20' : '21',
      ))
      return {}
    })
    const view = await mountCard()
    await settle()

    view.querySelector<HTMLButtonElement>('[data-testid="starter-funds-submit"]')!.click()
    await vi.waitFor(() => expect(fund).toHaveBeenCalledTimes(3))
    await settle()

    expect(fund.mock.calls.slice(0, 2).map((call) => call[0])).toEqual([
      'starter-v1-usdt',
      'starter-v1-usdt',
    ])
    expect(fund.mock.calls[2]?.[0]).toBe('starter-v1-btc')
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
  })

  it('reconciles a committed pending starter by GET without another POST', async () => {
    window.localStorage.setItem(
      PENDING_TRADING_WRITE_STORAGE_KEY,
      JSON.stringify(pendingStarter('starter-v1-usdt')),
    )
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'local:practice', github_login: 'practice', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    vi.mocked(tradingAPI.authCapabilities).mockResolvedValue({
      github_oauth_enabled: false,
      local_login_enabled: true,
      practice_mode_enabled: true,
      starter_funds_enabled: true,
      virtual_liquidity_enabled: true,
    })
    const funding = vi.spyOn(tradingAPI, 'fundingRequest').mockImplementation(async (id) => {
      if (id === 'starter-v1-usdt') return starterResult(id, '31')
      throw new TradingRequestError('not found', 'funding_request_not_found', 404, false)
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()
    await settle()
    funding.mockClear()

    view.querySelector<HTMLButtonElement>('[data-testid="funding-reconcile"]')!.click()
    await vi.waitFor(() => {
      expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
    })

    expect(funding.mock.calls).toEqual([['starter-v1-usdt']])
    expect(fund).not.toHaveBeenCalled()
  })

  it('reconciles a missing pending starter with one exact-ID POST between two GETs', async () => {
    window.localStorage.setItem(
      PENDING_TRADING_WRITE_STORAGE_KEY,
      JSON.stringify(pendingStarter('starter-v1-usdt')),
    )
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'local:practice', github_login: 'practice', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    vi.mocked(tradingAPI.authCapabilities).mockResolvedValue({
      github_oauth_enabled: false,
      local_login_enabled: true,
      practice_mode_enabled: true,
      starter_funds_enabled: true,
      virtual_liquidity_enabled: true,
    })
    let applied = false
    const trace: string[] = []
    vi.spyOn(tradingAPI, 'fundingRequest').mockImplementation(async (id) => {
      trace.push(`get:${id}`)
      if (id === 'starter-v1-usdt' && applied) return starterResult(id, '41')
      throw new TradingRequestError('not found', 'funding_request_not_found', 404, false)
    })
    const fund = vi.spyOn(tradingAPI, 'fund').mockImplementation(async (id) => {
      trace.push(`post:${id}`)
      applied = true
      return {}
    })
    const view = await mountCard()
    await settle()
    trace.length = 0

    view.querySelector<HTMLButtonElement>('[data-testid="funding-reconcile"]')!.click()
    await vi.waitFor(() => {
      expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
    })

    expect(trace).toEqual([
      'get:starter-v1-usdt',
      'post:starter-v1-usdt',
      'get:starter-v1-usdt',
    ])
    expect(fund).toHaveBeenCalledTimes(1)
    expect(fund).toHaveBeenCalledWith(
      'starter-v1-usdt', 'USDT', '10000', 'local:practice',
    )
  })

  it('does not render for a non-admin server session', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:reader', github_login: 'reader', admin: false },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()

    expect(view.querySelector('[data-testid="system-trading-admin"]')).toBeNull()
    expect(fund).not.toHaveBeenCalled()
  })

  it('shows the admin control but keeps funding disabled while recovery blocks writes', async () => {
    setLocale('zh-CN')
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const view = await mountCard(blocked)
    const amount = view.querySelector<HTMLInputElement>('input[name="amount"]')!
    setValue(amount, '100')
    await nextTick()

    expect(view.querySelector('[data-testid="system-trading-admin"]')).not.toBeNull()
    expect(view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')?.disabled)
      .toBe(true)
    expect(view.querySelector('[data-testid="funding-write-gate"]')).not.toBeNull()
    expect(view.textContent).toContain('恢复准入变为可写之前，虚拟入金保持禁用。')
    expect(view.textContent).toContain('虚拟入金')
  })

  it('reacts to the frozen English and Chinese System translation catalog', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const view = await mountCard()

    expect(view.textContent).toContain('Virtual funding')
    expect(view.textContent).toContain('Fund virtual assets')

    setLocale('zh-CN')
    await nextTick()

    expect(view.textContent).toContain('虚拟入金')
    expect(view.textContent).toContain('发放虚拟资金')
    expect(view.textContent).not.toContain('Admin-only learning funds')
  })

  it('rejects funding amounts outside the Go int64 atom domain', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()
    const amount = view.querySelector<HTMLInputElement>('input[name="amount"]')!
    setValue(amount, '9223372036854.775808')
    await nextTick()

    expect(view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')?.disabled)
      .toBe(true)
    expect(fund).not.toHaveBeenCalled()
  })

  it('re-reads the shared journal before funding and never overwrites a Trade claim', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()
    setValue(view.querySelector<HTMLInputElement>('input[name="amount"]')!, '100')
    await nextTick()
    expect(view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')?.disabled)
      .toBe(false)

    const tradeClaim: PendingTradingWrite = {
      operation_id: 'operation-trade-won-race',
      operation: 'submit',
      account_id: 'github:trader',
      request_id: 'submit-authoritative-id',
      state: 'unknown',
      created_at: 10,
      updated_at: 11,
      payload: { client_order_id: 'submit-authoritative-id' },
    }
    const authoritative = JSON.stringify(tradeClaim)
    // No storage event is dispatched: the System tab must still re-read the
    // shared authority immediately before claiming the journal.
    window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, authoritative)

    view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')!.click()
    await settle()

    expect(fund).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBe(authoritative)
    expect(view.textContent).toContain('Open Trade')
  })

  it('treats a malformed non-empty shared journal as a write lock', async () => {
    const malformed = '{"operation":"submit"'
    window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, malformed)
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()
    setValue(view.querySelector<HTMLInputElement>('input[name="amount"]')!, '100')
    await nextTick()

    expect(view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')?.disabled)
      .toBe(true)
    expect(view.textContent).toContain(
      'The pending operation journal could not be read. Writes remain blocked.',
    )
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBe(malformed)
    expect(fund).not.toHaveBeenCalled()
  })

  it('keeps a malformed legacy session journal locked across other-tab storage events', async () => {
    const malformed = '{"operation":"legacy-session"'
    window.sessionStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, malformed)
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()
    setValue(view.querySelector<HTMLInputElement>('input[name="amount"]')!, '100')

    const otherTab: PendingTradingWrite = {
      operation_id: 'operation-other-tab',
      operation: 'submit',
      account_id: 'github:trader',
      request_id: 'submit-other-tab',
      state: 'unknown',
      created_at: 20,
      updated_at: 21,
      payload: { client_order_id: 'submit-other-tab' },
    }
    const serialized = JSON.stringify(otherTab)
    window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, serialized)
    window.dispatchEvent(new StorageEvent('storage', {
      key: PENDING_TRADING_WRITE_STORAGE_KEY,
      newValue: serialized,
      storageArea: window.localStorage,
    }))
    window.localStorage.removeItem(PENDING_TRADING_WRITE_STORAGE_KEY)
    window.dispatchEvent(new StorageEvent('storage', {
      key: PENDING_TRADING_WRITE_STORAGE_KEY,
      oldValue: serialized,
      newValue: null,
      storageArea: window.localStorage,
    }))
    await nextTick()

    expect(view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')?.disabled)
      .toBe(true)
    view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')!.click()
    await settle()
    expect(fund).not.toHaveBeenCalled()
    expect(window.sessionStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBe(malformed)
  })

  it('persists before send and replays an unknown funding result with the original ID', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    let persistedBeforeSend: PendingTradingWrite | null = null
    const fund = vi.spyOn(tradingAPI, 'fund')
      .mockImplementationOnce((requestID) => {
        persistedBeforeSend = JSON.parse(
          window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY) ?? 'null',
        ) as PendingTradingWrite | null
        expect(persistedBeforeSend).toMatchObject({
          operation: 'fund',
          request_id: requestID,
          state: 'submitted',
        })
        return Promise.reject(new TradingRequestError(
          'network disconnected',
          'network_error',
          0,
          true,
        ))
      })
      .mockResolvedValueOnce({})
    const view = await mountCard()
    setValue(view.querySelector<HTMLInputElement>('input[name="account_id"]')!, 'github:beneficiary')
    setValue(view.querySelector<HTMLSelectElement>('select[name="asset"]')!, 'BTC')
    setValue(view.querySelector<HTMLInputElement>('input[name="amount"]')!, '0.01')
    await nextTick()

    view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')!.click()
    await settle()

    const stored = JSON.parse(
      window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY) ?? '{}',
    ) as PendingTradingWrite
    expect(stored.operation).toBe('fund')
    expect((persistedBeforeSend as PendingTradingWrite | null)?.request_id)
      .toBe(stored.request_id)
    expect(stored.account_id).toBe('github:admin')
    expect(stored.state).toBe('unknown')
    expect(stored.request_id).toMatch(/^fund-/)
    expect(stored.payload).toEqual({
      account_id: 'github:beneficiary',
      asset: 'BTC',
      amount: '0.01',
    })
    expect(fund).toHaveBeenNthCalledWith(
      1,
      stored.request_id,
      'BTC',
      '0.01',
      'github:beneficiary',
    )
    expect(view.textContent).toContain(
      `The outcome is unknown. Reconcile only with request ${stored.request_id}.`,
    )
    expect(view.textContent).not.toContain('network disconnected')

    view.querySelector<HTMLButtonElement>('[data-testid="funding-reconcile"]')!.click()
    await settle()

    expect(fund).toHaveBeenCalledTimes(2)
    expect(fund).toHaveBeenNthCalledWith(
      2,
      stored.request_id,
      'BTC',
      '0.01',
      'github:beneficiary',
    )
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
    expect(view.textContent).toContain(
      `The authoritative idempotent replay completed for ${stored.request_id}.`,
    )
  })

  it('does not expose an unknown backend failure to the administrator', async () => {
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    vi.spyOn(tradingAPI, 'fund').mockRejectedValue(
      new Error('operator-sensitive-internal-detail'),
    )
    const view = await mountCard()
    setValue(view.querySelector<HTMLInputElement>('input[name="amount"]')!, '100')
    await nextTick()

    view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')!.click()
    await settle()

    expect(view.textContent).toContain(
      'Virtual funding failed. Keep the original journal and inspect System evidence.',
    )
    expect(view.textContent).not.toContain('operator-sensitive-internal-detail')
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
  })

  it('does not overwrite a pending non-funding operation', async () => {
    const other: PendingTradingWrite = {
      operation_id: 'operation-submit',
      operation: 'submit',
      account_id: 'github:admin',
      request_id: 'submit-stable-id',
      state: 'unknown',
      created_at: 1,
      updated_at: 1,
      payload: { client_order_id: 'submit-stable-id' },
    }
    window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, JSON.stringify(other))
    vi.spyOn(tradingAPI, 'session').mockResolvedValue({
      principal: { account_id: 'github:admin', github_login: 'admin', admin: true },
      expires_at: '2099-01-01T00:00:00Z',
    })
    const fund = vi.spyOn(tradingAPI, 'fund')
    const view = await mountCard()

    expect(view.textContent).toContain('Open Trade')
    expect(view.querySelector<HTMLButtonElement>('[data-testid="funding-submit"]')?.disabled)
      .toBe(true)
    expect(fund).not.toHaveBeenCalled()
    expect(JSON.parse(
      window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY) ?? '{}',
    )).toMatchObject({
      operation: 'submit',
      request_id: 'submit-stable-id',
      state: 'unknown',
    })
  })
})
