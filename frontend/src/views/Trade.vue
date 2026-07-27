<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { CandlestickChart } from 'echarts/charts'
import {
  DataZoomComponent,
  GridComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import {
  getAssetDashboardV2,
  getAssetMarketsV2,
  getKlines,
  type Kline,
  type KlineInterval,
} from '../api/market'
import {
  eventSocketURL,
  TradingRequestError,
  tradingEventMode,
  tradingAPI,
  type AuthCapabilities,
  type Balance,
  type EventEnvelope,
  type Order,
  type OrderBook,
  type Principal,
  type Trade,
  type TradingStatus,
} from '../api/trading'
import PageHeader from '../components/PageHeader.vue'

echarts.use([
  CandlestickChart,
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
  CanvasRenderer,
])

const MARKET_ID = 'BTC-USDT'
const KLINE_INTERVALS: KlineInterval[] = ['1m', '15m', '1h', '1d']

const principal = ref<Principal | null>(null)
const authCapabilities = ref<AuthCapabilities>({
  github_oauth_enabled: false,
  local_login_enabled: false,
})
const book = ref<OrderBook>({ market_id: MARKET_ID, sequence: '0', bids: [], asks: [] })
const publicTrades = ref<Trade[]>([])
const privateTrades = ref<Trade[]>([])
const orders = ref<Order[]>([])
const balances = ref<Balance[]>([])
const status = ref<TradingStatus>({
  market_id: MARKET_ID,
  state: 'connecting',
  sequence: '0',
  queue_depth: 0,
  recovery_count: '0',
  last_error: '',
})
const errorMessage = ref('')
const notice = ref('')
const busy = ref(false)
const wsState = ref<'offline' | 'connecting' | 'live' | 'retrying' | 'polling'>('offline')
const cursor = ref<EventEnvelope>()
const lastStatusAt = ref(0)
const lastPrivateAt = ref(0)
const publicPanelErrors = reactive({ orderbook: '', trades: '', status: '' })
const unknownClientOrderID = ref('')

const referencePrice = ref<number | null>(null)
const referenceFreshness = ref('unavailable')
const referenceConfidence = ref('unknown')
const referenceObservedAt = ref(0)
const referenceError = ref('')
const klineMarketID = ref('')
const klineProvider = ref('')
const klineInterval = ref<KlineInterval>('15m')
const klines = ref<Kline[]>([])
const klineError = ref('')
const chartElement = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null

let socket: WebSocket | undefined
let reconnectTimer: number | undefined
let publicTimer: number | undefined
let marketTimer: number | undefined
let refreshTimer: number | undefined
let marketRefreshRunning = false

const form = reactive({
  clientOrderID: crypto.randomUUID(),
  side: 'buy',
  type: 'limit',
  timeInForce: 'gtc',
  postOnly: false,
  price: '',
  quantity: '0.01',
  quoteBudget: '100',
})
const funding = reactive({ asset: 'USDT', amount: '10000', accountID: '' })

const openOrders = computed(() => orders.value.filter((order) =>
  order.status === 'open' || order.status === 'partially_filled',
))
const historicalOrders = computed(() => orders.value.filter((order) =>
  order.status !== 'open' && order.status !== 'partially_filled',
))
const marketBuy = computed(() => form.type === 'market' && form.side === 'buy')
const marketSell = computed(() => form.type === 'market' && form.side === 'sell')
const loggedIn = computed(() => principal.value !== null)
const referenceIsFresh = computed(() =>
  referencePrice.value !== null && referenceFreshness.value === 'fresh',
)
const bestAsk = computed(() => book.value.asks[0]?.price ?? '')
const bestBid = computed(() => book.value.bids[0]?.price ?? '')
const statusAgeSeconds = computed(() =>
  lastStatusAt.value ? Math.max(0, Math.floor((Date.now() - lastStatusAt.value) / 1000)) : -1,
)
const matchingState = computed(() => {
  if (statusAgeSeconds.value < 0 || statusAgeSeconds.value > 10) return 'degraded'
  return status.value.state
})
const liquidityState = computed(() =>
  book.value.bids.length > 0 && book.value.asks.length > 0 ? 'active' : 'paused',
)
const transportState = computed(() => {
  const failures = Object.values(publicPanelErrors).filter(Boolean).length
  if (failures === 0 && statusAgeSeconds.value >= 0 && statusAgeSeconds.value <= 10) return 'live'
  if (lastStatusAt.value > 0) return 'degraded'
  return 'offline'
})
const writesEnabled = computed(() => {
  if (!loggedIn.value || busy.value || unknownClientOrderID.value) return false
  if (statusAgeSeconds.value < 0 || statusAgeSeconds.value > 10 || status.value.state !== 'ready') {
    return false
  }
  if (tradingEventMode() === 'polling') {
    return lastPrivateAt.value > 0 && Date.now() - lastPrivateAt.value <= 10_000
  }
  return wsState.value === 'live'
})

function randomID(prefix: string): string {
  return `${prefix}-${crypto.randomUUID()}`
}

function shortID(value: string): string {
  if (!value) return '—'
  return value.length > 18 ? `${value.slice(0, 9)}…${value.slice(-6)}` : value
}

function formatReference(value: number | null): string {
  if (value == null || !Number.isFinite(value)) return '—'
  return new Intl.NumberFormat('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value)
}

function formatObservedAt(value: number): string {
  if (!value) return 'No trusted observation'
  const milliseconds = value < 1e12 ? value * 1000 : value
  return new Date(milliseconds).toLocaleTimeString()
}

function resetClientOrderID(): void {
  form.clientOrderID = crypto.randomUUID()
}

function useReferencePrice(): void {
  if (!referenceIsFresh.value || referencePrice.value == null) return
  form.price = referencePrice.value.toFixed(2)
}

function useBookPrice(side: 'bid' | 'ask'): void {
  const value = side === 'bid' ? bestBid.value : bestAsk.value
  if (value) form.price = value
}

function showError(error: unknown): void {
  errorMessage.value = error instanceof Error ? error.message : String(error)
  window.setTimeout(() => {
    errorMessage.value = ''
  }, 6000)
}

async function loadPublic(): Promise<void> {
  const [bookResult, tradesResult, statusResult] = await Promise.allSettled([
    tradingAPI.orderBook(),
    tradingAPI.publicTrades(),
    tradingAPI.status(),
  ])
  if (bookResult.status === 'fulfilled') {
    const nextBook = bookResult.value
    book.value = {
      ...nextBook,
      bids: nextBook.bids ?? [],
      asks: nextBook.asks ?? [],
    }
    publicPanelErrors.orderbook = ''
  } else {
    publicPanelErrors.orderbook = bookResult.reason instanceof Error
      ? bookResult.reason.message
      : String(bookResult.reason)
  }
  if (tradesResult.status === 'fulfilled') {
    publicTrades.value = tradesResult.value.trades ?? []
    publicPanelErrors.trades = ''
  } else {
    publicPanelErrors.trades = tradesResult.reason instanceof Error
      ? tradesResult.reason.message
      : String(tradesResult.reason)
  }
  if (statusResult.status === 'fulfilled') {
    status.value = statusResult.value
    lastStatusAt.value = Date.now()
    publicPanelErrors.status = ''
  } else {
    publicPanelErrors.status = statusResult.reason instanceof Error
      ? statusResult.reason.message
      : String(statusResult.reason)
  }
  if (Object.values(publicPanelErrors).every(Boolean)) {
    showError('Qiu Market transport is offline; last known data remains visible.')
  }
}

async function loadPrivate(): Promise<void> {
  if (!principal.value) return
  const results = await Promise.allSettled([
    tradingAPI.balances(),
    tradingAPI.orders(false),
    tradingAPI.trades(),
  ])
  if (results[0].status === 'fulfilled') balances.value = results[0].value.balances ?? []
  if (results[1].status === 'fulfilled') orders.value = results[1].value.orders ?? []
  if (results[2].status === 'fulfilled') privateTrades.value = results[2].value.trades ?? []
  if (results.every((result) => result.status === 'fulfilled')) {
    lastPrivateAt.value = Date.now()
    if (
      unknownClientOrderID.value &&
      orders.value.some((order) => order.client_order_id === unknownClientOrderID.value)
    ) {
      notice.value = `请求 ${unknownClientOrderID.value} 已从权威订单视图确认`
      unknownClientOrderID.value = ''
      form.clientOrderID = crypto.randomUUID()
    }
  }
}

async function discoverSession(): Promise<void> {
  try {
    const session = await tradingAPI.session()
    principal.value = session.principal
    await loadPrivate()
    connectEvents()
  } catch {
    principal.value = null
  }
}

async function loadAuthCapabilities(): Promise<void> {
  try {
    authCapabilities.value = await tradingAPI.authCapabilities()
  } catch {
    authCapabilities.value = {
      github_oauth_enabled: false,
      local_login_enabled: false,
    }
  }
}

async function localLogin(): Promise<void> {
  busy.value = true
  try {
    const session = await tradingAPI.localLogin()
    principal.value = session.principal
    notice.value = '本地回环登录成功'
    await loadPrivate()
    connectEvents()
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function logout(): Promise<void> {
  try {
    await tradingAPI.logout()
  } catch (error) {
    showError(error)
  }
  principal.value = null
  balances.value = []
  orders.value = []
  privateTrades.value = []
  closeEvents()
}

async function submitOrder(): Promise<void> {
  busy.value = true
  notice.value = ''
  const submittedID = form.clientOrderID
  try {
    await tradingAPI.submit({
      client_order_id: form.clientOrderID,
      side: form.side,
      type: form.type,
      time_in_force: form.type === 'market' ? 'ioc' : form.timeInForce,
      post_only: form.type === 'limit' && form.postOnly,
      price: form.type === 'limit' ? form.price : '',
      quantity: marketBuy.value ? '' : form.quantity,
      quote_budget: marketBuy.value ? form.quoteBudget : '',
    })
    notice.value = `请求 ${submittedID} 已处理`
    form.clientOrderID = crypto.randomUUID()
    await refreshAll()
  } catch (error) {
    if (error instanceof TradingRequestError && error.uncertain) {
      unknownClientOrderID.value = submittedID
      notice.value = `请求 ${submittedID} 为 submitted/unknown；不会自动重下，正在查询权威订单事实`
      window.setTimeout(() => void loadPrivate(), 1500)
    } else {
      showError(error)
    }
  } finally {
    busy.value = false
  }
}

async function cancelOrder(order: Order): Promise<void> {
  try {
    await tradingAPI.cancel(order.id, randomID('cancel'))
    await refreshAll()
  } catch (error) {
    showError(error)
  }
}

async function fundVirtual(): Promise<void> {
  busy.value = true
  try {
    await tradingAPI.fund(
      randomID('fund'),
      funding.asset,
      funding.amount,
      funding.accountID,
    )
    notice.value = `已发放 ${funding.amount} ${funding.asset} 虚拟资金`
    await loadPrivate()
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function refreshAll(): Promise<void> {
  await Promise.all([loadPublic(), loadPrivate()])
}

function scheduleRefresh(): void {
  if (refreshTimer) window.clearTimeout(refreshTimer)
  refreshTimer = window.setTimeout(() => void refreshAll(), 80)
}

async function connectEvents(): Promise<void> {
  if (tradingEventMode() === 'polling') {
    wsState.value = 'polling'
    return
  }
  if (!principal.value || wsState.value === 'connecting' || wsState.value === 'live') return
  wsState.value = wsState.value === 'offline' ? 'connecting' : 'retrying'
  try {
    const issued = await tradingAPI.ticket()
    socket = new WebSocket(eventSocketURL(issued.ticket, cursor.value))
    socket.onopen = () => {
      wsState.value = 'live'
    }
    socket.onmessage = (message) => {
      const envelope = JSON.parse(message.data) as EventEnvelope
      cursor.value = envelope
      scheduleRefresh()
    }
    socket.onerror = () => socket?.close()
    socket.onclose = () => {
      socket = undefined
      if (!principal.value) {
        wsState.value = 'offline'
        return
      }
      wsState.value = 'retrying'
      reconnectTimer = window.setTimeout(() => void connectEvents(), 1200)
    }
  } catch (error) {
    showError(error)
    wsState.value = 'retrying'
    reconnectTimer = window.setTimeout(() => void connectEvents(), 2000)
  }
}

function closeEvents(): void {
  if (reconnectTimer) window.clearTimeout(reconnectTimer)
  reconnectTimer = undefined
  socket?.close()
  socket = undefined
  wsState.value = 'offline'
}

async function loadReference(): Promise<void> {
  try {
    const dashboard = await getAssetDashboardV2(1, 10, {
      venue: 'all',
      search: 'BTC',
      filter: 'assets',
      sortBy: 'rank',
      sortDirection: 'asc',
      universe: 'provider_union',
    })
    const btc = dashboard.items.find((item) => item.asset_symbol.toUpperCase() === 'BTC')
    if (!btc) throw new Error('BTC is absent from the current S78 provider selection')
    const value = btc.composite_price_usd.available
      ? btc.composite_price_usd.value
      : btc.price_usd.value
    referencePrice.value = value
    referenceFreshness.value = btc.freshness_status || 'unavailable'
    referenceConfidence.value = btc.confidence || 'unknown'
    referenceObservedAt.value = btc.last_success_at || btc.observed_at
    referenceError.value = value == null
      ? (btc.coverage_reason || 'No fresh CEX Spot contributor')
      : ''

    if (!klineMarketID.value) {
      const venues = await getAssetMarketsV2(btc.asset_id, 'all')
      const priority = ['binance', 'coinbase', 'bybit', 'okx']
      const source = venues
        .filter((item) => item.has_kline && item.market_type.toLowerCase() === 'spot')
        .sort((left, right) =>
          priority.indexOf(left.provider) - priority.indexOf(right.provider))[0]
      if (source) {
        klineMarketID.value = source.market_id
        klineProvider.value = source.provider
      } else {
        klineError.value = 'No reviewed BTC venue K-line is currently available'
      }
    }
    await loadKline()
  } catch (error) {
    referencePrice.value = null
    referenceFreshness.value = 'unavailable'
    referenceError.value = error instanceof Error ? error.message : String(error)
  }
}

async function refreshMarketData(): Promise<void> {
  if (marketRefreshRunning) return
  marketRefreshRunning = true
  try {
    await loadReference()
  } finally {
    marketRefreshRunning = false
  }
}

async function loadKline(): Promise<void> {
  if (!klineMarketID.value) return
  try {
    klines.value = await getKlines(klineMarketID.value, klineInterval.value, 160)
    klineError.value = klines.value.length === 0
      ? 'The selected venue returned no real candles'
      : ''
    await nextTick()
    renderChart()
  } catch (error) {
    klineError.value = error instanceof Error ? error.message : String(error)
    renderChart()
  }
}

function renderChart(): void {
  if (!chartElement.value) return
  if (!chart) chart = echarts.init(chartElement.value)
  if (klines.value.length === 0) {
    chart.clear()
    return
  }
  const values = klines.value.map((item) => [
    item.timestamp,
    item.open,
    item.close,
    item.low,
    item.high,
  ])
  chart.setOption({
    animation: false,
    grid: { left: 16, right: 58, top: 16, bottom: 42 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: '#ffffff',
      borderColor: '#d2d2d7',
      textStyle: { color: '#1d1d1f', fontSize: 12 },
    },
    xAxis: {
      type: 'time',
      axisLabel: { color: '#6e6e73', fontSize: 10, hideOverlap: true },
      axisLine: { lineStyle: { color: '#d2d2d7' } },
      splitLine: { show: false },
    },
    yAxis: {
      scale: true,
      position: 'right',
      axisLabel: { color: '#6e6e73', fontSize: 10 },
      splitLine: { lineStyle: { color: '#ececf0' } },
    },
    dataZoom: [
      { type: 'inside' },
      {
        type: 'slider',
        height: 16,
        bottom: 6,
        borderColor: '#d2d2d7',
        fillerColor: 'rgba(0,113,227,.12)',
        handleStyle: { color: '#0071e3' },
      },
    ],
    series: [{
      type: 'candlestick',
      data: values,
      itemStyle: {
        color: '#16825d',
        color0: '#d92d4c',
        borderColor: '#16825d',
        borderColor0: '#d92d4c',
      },
    }],
  }, true)
}

function resizeChart(): void {
  chart?.resize()
}

watch(klineInterval, () => void loadKline())

onMounted(async () => {
  await Promise.all([loadPublic(), refreshMarketData(), loadAuthCapabilities()])
  if (
    authCapabilities.value.github_oauth_enabled ||
    authCapabilities.value.local_login_enabled
  ) {
    await discoverSession()
  }
  publicTimer = window.setInterval(
    () => void (tradingEventMode() === 'polling' ? refreshAll() : loadPublic()),
    3000,
  )
  marketTimer = window.setInterval(() => void refreshMarketData(), 15_000)
  window.addEventListener('resize', resizeChart)
})

onBeforeUnmount(() => {
  if (publicTimer) window.clearInterval(publicTimer)
  if (marketTimer) window.clearInterval(marketTimer)
  if (refreshTimer) window.clearTimeout(refreshTimer)
  window.removeEventListener('resize', resizeChart)
  chart?.dispose()
  chart = null
  closeEvents()
})
</script>

<template>
  <div class="trade-page">
    <PageHeader
      title="BTC / USDT"
      subtitle="虚拟资金、确定性撮合、双重记账与事件流恢复；不连接充值、提现、私钥或真实交易所下单。"
    >
      <template #actions>
        <span class="badge badge--delayed">仅虚拟资金</span>
        <a
          v-if="!principal && authCapabilities.github_oauth_enabled"
          class="btn"
          href="/api/v1/trading/auth/github/start"
        >GitHub 登录</a>
        <button
          v-if="!principal && authCapabilities.local_login_enabled"
          class="btn btn--accent"
          :disabled="busy"
          @click="localLogin"
        >
          本地登录
        </button>
        <span
          v-if="!principal && !authCapabilities.github_oauth_enabled && !authCapabilities.local_login_enabled"
          class="badge badge--offline"
        >登录未配置</span>
        <button v-if="principal" class="btn" @click="logout">退出 {{ principal.github_login }}</button>
      </template>
    </PageHeader>

    <div v-if="errorMessage" class="trade-toast trade-toast--error">{{ errorMessage }}</div>
    <div v-if="notice" class="trade-toast trade-toast--success">{{ notice }}</div>

    <section class="trade-metrics">
      <article class="trade-metric card">
        <span>S78 综合参考价</span>
        <strong>{{ formatReference(referencePrice) }} <small>USDT</small></strong>
        <em :class="`freshness freshness--${referenceFreshness}`">
          {{ referenceFreshness }} · {{ referenceConfidence }}
        </em>
      </article>
      <article class="trade-metric card">
        <span>撮合运行时</span>
        <strong>{{ matchingState }}</strong>
        <em>sequence {{ status.sequence }} · age {{ statusAgeSeconds < 0 ? '—' : `${statusAgeSeconds}s` }}</em>
      </article>
      <article class="trade-metric card">
        <span>恢复与事件</span>
        <strong>{{ status.recovery_count }}</strong>
        <em>recovery count · hash replay guarded</em>
      </article>
      <article class="trade-metric card">
        <span>传输 / 流动性</span>
        <strong>{{ transportState }}</strong>
        <em>{{ wsState }} · liquidity {{ liquidityState }}</em>
      </article>
    </section>

    <section class="market-workspace">
      <article class="card trade-chart-card">
        <header class="trade-card-head">
          <div>
            <span>真实 venue K 线 · {{ klineProvider || 'unresolved' }}</span>
            <h2>BTC 市场上下文</h2>
          </div>
          <div class="intervals">
            <button
              v-for="value in KLINE_INTERVALS"
              :key="value"
              :class="{ active: klineInterval === value }"
              @click="klineInterval = value"
            >
              {{ value }}
            </button>
          </div>
        </header>
        <div v-if="klines.length" ref="chartElement" class="trade-chart"></div>
        <div v-else class="truth-empty">
          <strong>没有可用的真实 K 线</strong>
          <p>{{ klineError || referenceError || '等待 S78 行情读模型返回可信数据。' }}</p>
          <span>NO MOCK · NO STATIC FALLBACK</span>
        </div>
        <footer class="reference-foot">
          <span>{{ referenceError || 'Composite Spot reference from eligible CEX contributors' }}</span>
          <span>{{ formatObservedAt(referenceObservedAt) }}</span>
        </footer>
      </article>

      <article class="card orderbook-card">
        <header class="trade-card-head">
          <div><span>虚拟订单簿</span><h2>Depth · 20</h2></div>
          <code>#{{ book.sequence }}</code>
        </header>
        <div class="book-heading"><span>价格 USDT</span><span>数量 BTC</span><span>订单</span></div>
        <div class="book-side book-side--asks">
          <button
            v-for="level in [...book.asks].reverse()"
            :key="`ask-${level.price}`"
            class="book-line"
            @click="form.price = level.price"
          >
            <span>{{ level.price }}</span><span>{{ level.quantity }}</span><span>{{ level.order_count }}</span>
          </button>
          <div v-if="!book.asks.length" class="empty-line">暂无卖单</div>
        </div>
        <div class="book-spread">
          <button :disabled="!bestBid" @click="useBookPrice('bid')">Bid {{ bestBid || '—' }}</button>
          <strong>{{ matchingState.toUpperCase() }}</strong>
          <button :disabled="!bestAsk" @click="useBookPrice('ask')">Ask {{ bestAsk || '—' }}</button>
        </div>
        <div class="book-side book-side--bids">
          <button
            v-for="level in book.bids"
            :key="`bid-${level.price}`"
            class="book-line"
            @click="form.price = level.price"
          >
            <span>{{ level.price }}</span><span>{{ level.quantity }}</span><span>{{ level.order_count }}</span>
          </button>
          <div v-if="!book.bids.length" class="empty-line">暂无买单</div>
        </div>
      </article>
    </section>

    <section class="account-workspace">
      <article class="card order-entry-card">
        <header class="trade-card-head">
          <div><span>订单入口</span><h2>创建虚拟订单</h2></div>
          <span class="badge" :class="loggedIn ? 'badge--live' : 'badge--offline'">
            {{ loggedIn ? '身份已绑定' : '需要登录' }}
          </span>
        </header>
        <div class="entry-body">
          <div class="side-switch">
            <button :class="{ active: form.side === 'buy' }" @click="form.side = 'buy'">买入</button>
            <button :class="{ active: form.side === 'sell' }" @click="form.side = 'sell'">卖出</button>
          </div>
          <div class="type-switch">
            <button :class="{ active: form.type === 'limit' }" @click="form.type = 'limit'">Limit</button>
            <button :class="{ active: form.type === 'market' }" @click="form.type = 'market'">Market</button>
          </div>
          <label>
            Client Order ID
            <span class="input-pair">
              <input v-model="form.clientOrderID" class="input" autocomplete="off" />
              <button class="btn" :disabled="Boolean(unknownClientOrderID)" @click="resetClientOrderID">重置</button>
            </span>
          </label>
          <label v-if="form.type === 'limit'">
            价格 · USDT
            <span class="input-pair">
              <input v-model="form.price" class="input" inputmode="decimal" placeholder="输入限价" />
              <button class="btn" :disabled="!referenceIsFresh" @click="useReferencePrice">参考价</button>
            </span>
          </label>
          <label v-if="!marketBuy">
            数量 · BTC
            <input v-model="form.quantity" class="input" inputmode="decimal" />
          </label>
          <label v-if="marketBuy">
            Quote Budget · USDT
            <input v-model="form.quoteBudget" class="input" inputmode="decimal" />
          </label>
          <div v-if="form.type === 'limit'" class="tif-row">
            <label>
              Time in force
              <select v-model="form.timeInForce" class="input">
                <option value="gtc">GTC</option>
                <option value="ioc">IOC</option>
                <option value="fok">FOK</option>
              </select>
            </label>
            <label class="checkbox">
              <input v-model="form.postOnly" type="checkbox" />
              Post Only
            </label>
          </div>
          <p v-if="marketSell" class="entry-hint">Market Sell 使用 BTC 数量，并固定为 IOC。</p>
          <button
            class="submit-order"
            :class="`submit-order--${form.side}`"
            :disabled="!writesEnabled"
            @click="submitOrder"
          >
            {{ unknownClientOrderID
              ? '订单状态确认中'
              : loggedIn
                ? `${form.side === 'buy' ? '买入' : '卖出'} BTC`
                : '登录后下单' }}
          </button>
        </div>
      </article>

      <article class="card balance-card">
        <header class="trade-card-head">
          <div><span>账户视图</span><h2>余额与虚拟入金</h2></div>
          <span class="badge badge--delayed">ADMIN · VIRTUAL</span>
        </header>
        <div class="balance-body">
          <div class="balances">
            <div v-for="balance in balances" :key="balance.asset" class="asset-balance">
              <span>{{ balance.asset }}</span>
              <strong>{{ balance.available }}</strong>
              <small>held {{ balance.held }}</small>
            </div>
            <div v-if="!balances.length" class="empty-line balance-empty">
              {{ loggedIn ? '余额尚未加载' : '登录后查看账户余额' }}
            </div>
          </div>
          <div class="fund-grid">
            <label>资产
              <select v-model="funding.asset" class="input"><option>USDT</option><option>BTC</option></select>
            </label>
            <label>数量
              <input v-model="funding.amount" class="input" inputmode="decimal" />
            </label>
            <label>目标账户（留空为自己）
              <input v-model="funding.accountID" class="input" placeholder="github:qianqiu0404" />
            </label>
            <button class="btn btn--accent" :disabled="!principal?.admin || !writesEnabled" @click="fundVirtual">
              发放虚拟资金
            </button>
          </div>
          <div class="runtime-grid">
            <span>恢复次数 <strong>{{ status.recovery_count }}</strong></span>
            <span>事件序列 <strong>{{ status.sequence }}</strong></span>
            <span>WS 重连 <strong>{{ wsState }}</strong></span>
          </div>
        </div>
      </article>

      <article class="card recent-card">
        <header class="trade-card-head">
          <div><span>公开市场</span><h2>最近成交</h2></div>
          <span class="badge" :class="wsState === 'live' ? 'badge--live' : 'badge--delayed'">{{ wsState }}</span>
        </header>
        <div class="recent-list">
          <div v-for="trade in publicTrades" :key="trade.id" class="recent-row">
            <div><strong>{{ trade.price }}</strong><small>USDT</small></div>
            <div><span>{{ trade.quantity }}</span><small>BTC</small></div>
            <code>{{ shortID(trade.id) }}</code>
          </div>
          <div v-if="!publicTrades.length" class="empty-line">尚未发生虚拟成交</div>
        </div>
      </article>
    </section>

    <section class="card records-card">
      <header class="trade-card-head">
        <div><span>订单与成交</span><h2>我的交易记录</h2></div>
        <button class="btn" :disabled="!loggedIn" @click="refreshAll">刷新</button>
      </header>
      <div class="record-block">
        <h3>当前委托 · {{ openOrders.length }}</h3>
        <div class="record-table">
          <div class="record-head"><span>订单</span><span>方向</span><span>价格</span><span>剩余</span><span>状态</span><span></span></div>
          <div v-for="order in openOrders" :key="order.id" class="record-row">
            <code>{{ shortID(order.id) }}</code><span :class="order.side">{{ order.side }}</span>
            <span>{{ order.price || 'Market' }}</span><span>{{ order.remaining_quantity }}</span>
            <span>{{ order.status }}</span><button class="cancel-link" :disabled="!writesEnabled" @click="cancelOrder(order)">撤单</button>
          </div>
          <div v-if="!openOrders.length" class="empty-line">暂无当前委托</div>
        </div>
      </div>
      <div class="record-block">
        <h3>历史委托 · {{ historicalOrders.length }}</h3>
        <div class="record-table">
          <div class="record-head"><span>订单</span><span>方向</span><span>成交</span><span>花费</span><span>状态</span><span>序列</span></div>
          <div v-for="order in historicalOrders" :key="order.id" class="record-row">
            <code>{{ shortID(order.id) }}</code><span :class="order.side">{{ order.side }}</span>
            <span>{{ order.filled_quantity }}</span><span>{{ order.spent_quote }}</span>
            <span>{{ order.status }}</span><span>#{{ order.last_sequence }}</span>
          </div>
          <div v-if="!historicalOrders.length" class="empty-line">暂无历史委托</div>
        </div>
      </div>
      <div class="record-block">
        <h3>我的成交 · {{ privateTrades.length }}</h3>
        <div class="record-table">
          <div class="record-head"><span>成交</span><span>价格</span><span>数量</span><span>Quote</span><span>买方费</span><span>卖方费</span></div>
          <div v-for="trade in privateTrades" :key="trade.id" class="record-row">
            <code>{{ shortID(trade.id) }}</code><span>{{ trade.price }}</span>
            <span>{{ trade.quantity }}</span><span>{{ trade.quote_amount }}</span>
            <span>{{ trade.buyer_fee?.amount }} {{ trade.buyer_fee?.asset }}</span>
            <span>{{ trade.seller_fee?.amount }} {{ trade.seller_fee?.asset }}</span>
          </div>
          <div v-if="!privateTrades.length" class="empty-line">暂无个人成交</div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.trade-page {
  display: grid;
  gap: 16px;
  max-width: 1540px;
  margin: 0 auto;
}

.trade-toast {
  position: fixed;
  top: 72px;
  left: 50%;
  z-index: 90;
  transform: translateX(-50%);
  width: min(560px, calc(100vw - 32px));
  padding: 12px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-float);
}

.trade-toast--error { color: var(--down); background: #fff0f2; }
.trade-toast--success { color: var(--up); background: #e9f7f1; }

.trade-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.trade-metric {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px 18px;
}

.trade-metric > span,
.trade-card-head span {
  color: var(--text-3);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: .04em;
  text-transform: uppercase;
}

.trade-metric strong {
  font-size: 21px;
  font-variant-numeric: tabular-nums;
}

.trade-metric strong small { color: var(--text-3); font-size: 11px; }
.trade-metric em { color: var(--text-3); font-size: 11px; font-style: normal; }
.freshness--fresh { color: var(--up) !important; }
.freshness--stale { color: var(--warn) !important; }
.freshness--unavailable { color: var(--down) !important; }

.market-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1.6fr) minmax(330px, .7fr);
  gap: 14px;
}

.account-workspace {
  display: grid;
  grid-template-columns: minmax(320px, .8fr) minmax(430px, 1.1fr) minmax(260px, .7fr);
  gap: 14px;
}

.trade-chart-card,
.orderbook-card,
.order-entry-card,
.balance-card,
.recent-card,
.records-card {
  overflow: hidden;
}

.trade-card-head {
  min-height: 64px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.trade-card-head h2 { margin-top: 3px; font-size: 16px; }
.trade-card-head code { color: var(--text-3); font-size: 11px; }

.intervals,
.side-switch,
.type-switch {
  display: flex;
  padding: 3px;
  gap: 2px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg-panel-2);
}

.intervals button,
.side-switch button,
.type-switch button {
  border: 0;
  border-radius: 7px;
  color: var(--text-2);
  background: transparent;
  padding: 6px 10px;
  cursor: pointer;
}

.intervals button.active,
.side-switch button.active,
.type-switch button.active {
  color: var(--accent);
  background: var(--bg-panel);
  box-shadow: 0 1px 5px rgba(0, 36, 77, .12);
}

.side-switch button { flex: 1; }
.side-switch button:first-child.active { color: var(--up); }
.side-switch button:last-child.active { color: var(--down); }
.type-switch button { flex: 1; }

.trade-chart { height: 390px; width: 100%; }

.truth-empty {
  min-height: 390px;
  padding: 28px;
  display: grid;
  place-content: center;
  text-align: center;
  background:
    linear-gradient(var(--border) 1px, transparent 1px),
    linear-gradient(90deg, var(--border) 1px, transparent 1px);
  background-size: 100% 60px, 80px 100%;
}

.truth-empty strong { font-size: 17px; }
.truth-empty p { max-width: 460px; color: var(--text-2); }
.truth-empty span { color: var(--down); font-size: 10px; font-weight: 700; letter-spacing: .1em; }

.reference-foot {
  min-height: 40px;
  padding: 9px 18px;
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: var(--text-3);
  font-size: 10px;
}

.book-heading,
.book-line {
  display: grid;
  grid-template-columns: 1.2fr 1fr .45fr;
  gap: 8px;
  padding: 6px 16px;
  text-align: right;
  font: 11px var(--font-mono);
}

.book-heading { padding-top: 12px; color: var(--text-3); }
.book-line { width: 100%; border: 0; background: transparent; color: var(--text-1); cursor: pointer; }
.book-line:hover { background: var(--bg-panel-2); }
.book-line span:first-child { text-align: left; }
.book-side { min-height: 128px; }
.book-side--asks .book-line span:first-child { color: var(--down); }
.book-side--bids .book-line span:first-child { color: var(--up); }

.book-spread {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  gap: 8px;
  align-items: center;
  padding: 9px 16px;
  border-block: 1px solid var(--border);
  background: var(--bg-panel-2);
}

.book-spread button { border: 0; background: transparent; color: var(--text-3); font-size: 10px; }
.book-spread button:last-child { text-align: right; }
.book-spread strong { color: var(--accent); font-size: 11px; }

.empty-line {
  padding: 18px;
  color: var(--text-3);
  font-size: 12px;
  text-align: center;
}

.entry-body,
.balance-body { padding: 16px 18px 18px; }
.entry-body { display: grid; gap: 12px; }
.entry-body label,
.fund-grid label {
  display: grid;
  gap: 6px;
  color: var(--text-2);
  font-size: 11px;
  font-weight: 600;
}

.input-pair { display: grid; grid-template-columns: 1fr auto; gap: 7px; }
.tif-row { display: grid; grid-template-columns: 1fr auto; gap: 12px; align-items: end; }
.checkbox { display: flex !important; align-items: center; padding-bottom: 10px; }
.checkbox input { accent-color: var(--accent); }
.entry-hint { margin: 0; color: var(--text-3); font-size: 11px; }

.submit-order {
  min-height: 44px;
  border: 0;
  border-radius: var(--radius-sm);
  color: #fff;
  font-weight: 600;
  cursor: pointer;
}

.submit-order--buy { background: var(--up); }
.submit-order--sell { background: var(--down); }
.submit-order:disabled { opacity: .45; cursor: not-allowed; }

.balances { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.asset-balance {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-panel-2);
}
.asset-balance span { color: var(--text-3); font-size: 11px; font-weight: 600; }
.asset-balance strong { font: 18px var(--font-mono); }
.asset-balance small { color: var(--text-3); font: 10px var(--font-mono); }
.balance-empty { grid-column: 1 / -1; }

.fund-grid {
  display: grid;
  grid-template-columns: .5fr .7fr 1.3fr;
  gap: 8px;
  align-items: end;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}
.fund-grid > .btn {
  grid-column: 1 / -1;
  white-space: nowrap;
}

.runtime-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin-top: 14px;
}
.runtime-grid span {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 9px 10px;
  border-radius: 8px;
  color: var(--text-3);
  background: var(--bg-panel-2);
  font-size: 10px;
}
.runtime-grid strong { color: var(--text-1); }

.recent-list { max-height: 390px; overflow: auto; }
.recent-row {
  display: grid;
  grid-template-columns: 1fr 1fr 1.2fr;
  gap: 8px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
}
.recent-row div { display: flex; flex-direction: column; }
.recent-row strong { color: var(--up); font: 12px var(--font-mono); }
.recent-row span,
.recent-row code { font: 11px var(--font-mono); }
.recent-row small,
.recent-row code { color: var(--text-3); }

.records-card { display: grid; }
.record-block { padding: 16px 18px 0; }
.record-block:last-child { padding-bottom: 18px; }
.record-block h3 { margin-bottom: 8px; color: var(--text-2); font-size: 13px; }
.record-table { overflow-x: auto; border: 1px solid var(--border); border-radius: var(--radius-sm); }
.record-head,
.record-row {
  display: grid;
  grid-template-columns: 1.35fr .55fr .8fr .8fr .9fr .65fr;
  gap: 12px;
  align-items: center;
  min-width: 760px;
  padding: 9px 12px;
  font-size: 11px;
}
.record-head { color: var(--text-3); background: var(--bg-panel-2); font-weight: 600; }
.record-row { border-top: 1px solid var(--border); }
.record-row code { color: var(--text-3); }
.record-row .buy { color: var(--up); }
.record-row .sell { color: var(--down); }
.cancel-link { border: 0; background: transparent; color: var(--down); cursor: pointer; }

@media (max-width: 1280px) {
  .trade-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .account-workspace { grid-template-columns: 1fr 1fr; }
  .recent-card { grid-column: 1 / -1; }
}

@media (max-width: 980px) {
  .market-workspace,
  .account-workspace { grid-template-columns: 1fr; }
  .recent-card { grid-column: auto; }
}

@media (max-width: 700px) {
  .trade-metrics { grid-template-columns: 1fr; }
  .trade-card-head { align-items: flex-start; flex-direction: column; }
  .fund-grid,
  .runtime-grid { grid-template-columns: 1fr; }
  .reference-foot { flex-direction: column; }
}
</style>
