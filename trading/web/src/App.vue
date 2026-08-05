<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { eventSocketURL, tradingAPI } from './api'
import type {
  Balance,
  EventEnvelope,
  Order,
  OrderBook,
  Principal,
  Status,
  Trade,
} from './types'

const principal = ref<Principal | null>(null)
const book = ref<OrderBook>({ market_id: 'BTC-USDT', sequence: '0', bids: [], asks: [] })
const publicTrades = ref<Trade[]>([])
const privateTrades = ref<Trade[]>([])
const orders = ref<Order[]>([])
const balances = ref<Balance[]>([])
const status = ref<Status>({
  market_id: 'BTC-USDT',
  state: 'connecting',
  sequence: '0',
  queue_depth: 0,
  recovery_count: '0',
  last_error: '',
})
const errorMessage = ref('')
const notice = ref('')
const busy = ref(false)
const wsState = ref<'offline' | 'connecting' | 'live' | 'retrying'>('offline')
const cursor = ref<EventEnvelope>()
let socket: WebSocket | undefined
let reconnectTimer: number | undefined
let publicTimer: number | undefined
let refreshTimer: number | undefined

const form = reactive({
  clientOrderID: crypto.randomUUID(),
  side: 'buy',
  type: 'limit',
  timeInForce: 'gtc',
  postOnly: false,
  price: '60000',
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

function randomID(prefix: string): string {
  return `${prefix}-${crypto.randomUUID()}`
}

function resetClientOrderID(): void {
  form.clientOrderID = crypto.randomUUID()
}

function showError(error: unknown): void {
  errorMessage.value = error instanceof Error ? error.message : String(error)
  window.setTimeout(() => {
    errorMessage.value = ''
  }, 6000)
}

async function loadPublic(): Promise<void> {
  try {
    const [nextBook, trades, nextStatus] = await Promise.all([
      tradingAPI.orderBook(),
      tradingAPI.publicTrades(),
      tradingAPI.status(),
    ])
    book.value = nextBook
    publicTrades.value = trades.trades ?? []
    status.value = nextStatus
  } catch (error) {
    showError(error)
  }
}

async function loadPrivate(): Promise<void> {
  if (!principal.value) return
  try {
    const [nextBalances, nextOrders, nextTrades] = await Promise.all([
      tradingAPI.balances(),
      tradingAPI.orders(false),
      tradingAPI.trades(),
    ])
    balances.value = nextBalances.balances ?? []
    orders.value = nextOrders.orders ?? []
    privateTrades.value = nextTrades.trades ?? []
  } catch (error) {
    showError(error)
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
    notice.value = `请求 ${form.clientOrderID} 已处理`
    form.clientOrderID = crypto.randomUUID()
    await refreshAll()
  } catch (error) {
    showError(error)
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

function shortID(value: string): string {
  if (!value) return '—'
  return value.length > 18 ? `${value.slice(0, 9)}…${value.slice(-6)}` : value
}

onMounted(async () => {
  await loadPublic()
  await discoverSession()
  publicTimer = window.setInterval(() => void loadPublic(), 3000)
})

onBeforeUnmount(() => {
  if (publicTimer) window.clearInterval(publicTimer)
  if (refreshTimer) window.clearTimeout(refreshTimer)
  closeEvents()
})
</script>

<template>
  <main class="shell">
    <header class="topbar">
      <div class="identity">
        <div class="mark">Q</div>
        <div>
          <p class="eyebrow">S78 · VIRTUAL SPOT</p>
          <h1>BTC / USDT</h1>
        </div>
        <span class="virtual-badge">仅虚拟资金</span>
      </div>
      <div class="top-actions">
        <div class="health" :class="status.state">
          <span class="pulse"></span>
          {{ status.state }} · seq {{ status.sequence }}
        </div>
        <template v-if="principal">
          <div class="account">
            <span>{{ principal.github_login }}</span>
            <small>{{ shortID(principal.account_id) }}</small>
          </div>
          <button class="ghost" @click="logout">退出</button>
        </template>
        <template v-else>
          <a class="ghost" href="/api/v1/trading/auth/github/start">GitHub 登录</a>
          <button class="primary compact" :disabled="busy" @click="localLogin">
            本地登录
          </button>
        </template>
      </div>
    </header>

    <div v-if="errorMessage" class="toast error">{{ errorMessage }}</div>
    <div v-if="notice" class="toast success">{{ notice }}</div>

    <section class="market-grid">
      <article class="panel chart-panel">
        <div class="panel-head">
          <div>
            <p class="label">参考行情与 K 线</p>
            <h2>行情接口等待集成</h2>
          </div>
          <span class="truth-badge">NO FALLBACK</span>
        </div>
        <div class="chart-empty">
          <div class="chart-lines"></div>
          <strong>暂无可信参考价</strong>
          <p>
            等待 MacBook Air 的 S78 行情基线完成并接入。这里不会使用随机数、
            静态价格或过期行情填充 K 线。
          </p>
        </div>
      </article>

      <article class="panel book-panel">
        <div class="panel-head">
          <div>
            <p class="label">虚拟订单簿</p>
            <h2>Depth · 20</h2>
          </div>
          <span class="sequence">#{{ book.sequence }}</span>
        </div>
        <div class="book-header"><span>价格 (USDT)</span><span>数量 (BTC)</span><span>订单</span></div>
        <div class="book asks">
          <div v-for="level in [...book.asks].reverse()" :key="`a-${level.price}`" class="book-row">
            <span>{{ level.price }}</span><span>{{ level.quantity }}</span><span>{{ level.order_count }}</span>
          </div>
          <div v-if="!book.asks.length" class="empty-row">暂无卖单</div>
        </div>
        <div class="spread">
          <span>撮合引擎</span>
          <strong>{{ status.state === 'ready' ? 'READY' : status.state.toUpperCase() }}</strong>
          <small>queue {{ status.queue_depth }}</small>
        </div>
        <div class="book bids">
          <div v-for="level in book.bids" :key="`b-${level.price}`" class="book-row">
            <span>{{ level.price }}</span><span>{{ level.quantity }}</span><span>{{ level.order_count }}</span>
          </div>
          <div v-if="!book.bids.length" class="empty-row">暂无买单</div>
        </div>
      </article>

      <article class="panel trades-panel">
        <div class="panel-head">
          <div>
            <p class="label">公开市场</p>
            <h2>最近成交</h2>
          </div>
          <span class="ws" :class="wsState">{{ wsState }}</span>
        </div>
        <div class="trade-list">
          <div v-for="trade in publicTrades" :key="trade.id" class="trade-row">
            <div><strong>{{ trade.price }}</strong><small>USDT</small></div>
            <div><span>{{ trade.quantity }}</span><small>BTC</small></div>
            <code>{{ shortID(trade.id) }}</code>
          </div>
          <div v-if="!publicTrades.length" class="empty-state">尚未发生虚拟成交</div>
        </div>
      </article>
    </section>

    <section class="workspace-grid">
      <article class="panel order-entry">
        <div class="panel-head">
          <div>
            <p class="label">订单入口</p>
            <h2>创建虚拟订单</h2>
          </div>
          <span class="lock">{{ loggedIn ? '身份已绑定' : '需要登录' }}</span>
        </div>
        <div class="segmented two">
          <button :class="{ active: form.side === 'buy' }" @click="form.side = 'buy'">买入</button>
          <button :class="{ active: form.side === 'sell' }" @click="form.side = 'sell'">卖出</button>
        </div>
        <div class="segmented">
          <button :class="{ active: form.type === 'limit' }" @click="form.type = 'limit'">Limit</button>
          <button :class="{ active: form.type === 'market' }" @click="form.type = 'market'">Market</button>
        </div>
        <label>
          Client Order ID
          <div class="input-action">
            <input v-model="form.clientOrderID" autocomplete="off" />
            <button @click="resetClientOrderID">重置</button>
          </div>
        </label>
        <label v-if="form.type === 'limit'">
          价格 · USDT
          <input v-model="form.price" inputmode="decimal" />
        </label>
        <label v-if="!marketBuy">
          数量 · BTC
          <input v-model="form.quantity" inputmode="decimal" />
        </label>
        <label v-if="marketBuy">
          Quote Budget · USDT
          <input v-model="form.quoteBudget" inputmode="decimal" />
        </label>
        <div v-if="form.type === 'limit'" class="tif-row">
          <label>
            Time in force
            <select v-model="form.timeInForce">
              <option value="gtc">GTC</option>
              <option value="ioc">IOC</option>
              <option value="fok">FOK</option>
            </select>
          </label>
          <label class="check">
            <input v-model="form.postOnly" type="checkbox" />
            Post Only
          </label>
        </div>
        <p v-if="marketSell" class="hint">Market Sell 使用 BTC 数量，并固定为 IOC。</p>
        <button
          class="submit"
          :class="form.side"
          :disabled="busy || !loggedIn"
          @click="submitOrder"
        >
          {{ loggedIn ? `${form.side === 'buy' ? '买入' : '卖出'} BTC` : '登录后下单' }}
        </button>
      </article>

      <article class="panel account-panel">
        <div class="panel-head">
          <div>
            <p class="label">账户视图</p>
            <h2>余额与虚拟入金</h2>
          </div>
          <span class="admin">ADMIN · VIRTUAL</span>
        </div>
        <div class="balance-grid">
          <div v-for="balance in balances" :key="balance.asset" class="balance-card">
            <span>{{ balance.asset }}</span>
            <strong>{{ balance.available }}</strong>
            <small>held {{ balance.held }}</small>
          </div>
          <div v-if="!balances.length" class="empty-state wide">
            {{ loggedIn ? '余额尚未加载' : '登录后查看账户余额' }}
          </div>
        </div>
        <div class="funding">
          <label>资产
            <select v-model="funding.asset">
              <option>USDT</option>
              <option>BTC</option>
            </select>
          </label>
          <label>数量
            <input v-model="funding.amount" inputmode="decimal" />
          </label>
          <label>目标账户（留空为自己）
            <input v-model="funding.accountID" placeholder="github:qianqiu0404" />
          </label>
          <button class="primary" :disabled="busy || !principal?.admin" @click="fundVirtual">
            发放虚拟资金
          </button>
        </div>
        <div class="runtime">
          <div><span>恢复次数</span><strong>{{ status.recovery_count }}</strong></div>
          <div><span>事件序列</span><strong>{{ status.sequence }}</strong></div>
          <div><span>WebSocket</span><strong>{{ wsState }}</strong></div>
          <p v-if="status.last_error">{{ status.last_error }}</p>
        </div>
      </article>
    </section>

    <section class="panel orders-panel">
      <div class="panel-head">
        <div>
          <p class="label">订单与成交</p>
          <h2>我的交易记录</h2>
        </div>
        <button class="ghost" :disabled="!loggedIn" @click="refreshAll">刷新</button>
      </div>
      <div class="tabs-content">
        <div class="table-block">
          <h3>当前委托 · {{ openOrders.length }}</h3>
          <div class="data-table">
            <div class="table-head"><span>订单</span><span>方向</span><span>价格</span><span>剩余</span><span>状态</span><span></span></div>
            <div v-for="order in openOrders" :key="order.id" class="table-row">
              <code>{{ shortID(order.id) }}</code>
              <span :class="order.side">{{ order.side }}</span>
              <span>{{ order.price || 'Market' }}</span>
              <span>{{ order.remaining_quantity }}</span>
              <span>{{ order.status }}</span>
              <button class="danger-link" @click="cancelOrder(order)">撤单</button>
            </div>
            <div v-if="!openOrders.length" class="empty-row">暂无当前委托</div>
          </div>
        </div>
        <div class="table-block">
          <h3>历史委托 · {{ historicalOrders.length }}</h3>
          <div class="data-table">
            <div class="table-head"><span>订单</span><span>方向</span><span>成交</span><span>花费</span><span>状态</span><span>序列</span></div>
            <div v-for="order in historicalOrders" :key="order.id" class="table-row">
              <code>{{ shortID(order.id) }}</code>
              <span :class="order.side">{{ order.side }}</span>
              <span>{{ order.filled_quantity }}</span>
              <span>{{ order.spent_quote }}</span>
              <span>{{ order.status }}</span>
              <span>#{{ order.last_sequence }}</span>
            </div>
            <div v-if="!historicalOrders.length" class="empty-row">暂无历史委托</div>
          </div>
        </div>
        <div class="table-block">
          <h3>我的成交 · {{ privateTrades.length }}</h3>
          <div class="data-table trades">
            <div class="table-head"><span>成交</span><span>价格</span><span>数量</span><span>Quote</span><span>买方费</span><span>卖方费</span></div>
            <div v-for="trade in privateTrades" :key="trade.id" class="table-row">
              <code>{{ shortID(trade.id) }}</code>
              <span>{{ trade.price }}</span>
              <span>{{ trade.quantity }}</span>
              <span>{{ trade.quote_amount }}</span>
              <span>{{ trade.buyer_fee?.amount }} {{ trade.buyer_fee?.asset }}</span>
              <span>{{ trade.seller_fee?.amount }} {{ trade.seller_fee?.asset }}</span>
            </div>
            <div v-if="!privateTrades.length" class="empty-row">暂无个人成交</div>
          </div>
        </div>
      </div>
    </section>

    <footer>
      <strong>S78 Trading Lab</strong>
      <span>虚拟资金 · 确定性撮合 · 事件流恢复</span>
      <span>不连接充值、提现、私钥或真实交易 API</span>
    </footer>
  </main>
</template>
