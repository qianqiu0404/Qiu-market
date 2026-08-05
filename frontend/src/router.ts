import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/markets' },
  { path: '/dashboard', redirect: '/markets' },
  {
    path: '/markets',
    name: 'markets',
    component: () => import('./views/Markets.vue'),
    meta: { title: 'Markets' },
  },
  {
    path: '/markets/:marketId',
    name: 'market-detail',
    component: () => import('./views/Klines.vue'),
    meta: { title: 'Market Chart' },
  },
  { path: '/klines', redirect: '/markets' },
  {
    path: '/insights',
    name: 'insights',
    component: () => import('./views/Insights.vue'),
    meta: { title: 'Insights' },
  },
  {
    path: '/trade/BTC-USDT',
    name: 'trade-btc-usdt',
    component: () => import('./views/Trade.vue'),
    meta: { title: 'Virtual Spot' },
  },
  { path: '/analytics', redirect: '/insights' },
  {
    path: '/system',
    name: 'system',
    component: () => import('./views/System.vue'),
    meta: { title: 'System' },
  },
  { path: '/pipeline', redirect: '/system' },
  { path: '/assets', redirect: { path: '/system', query: { tab: 'assets' } } },
  { path: '/exchanges', redirect: { path: '/system', query: { tab: 'exchanges' } } },
  { path: '/symbols', redirect: { path: '/system', query: { tab: 'symbols' } } },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('./views/NotFound.vue'),
    meta: { title: 'Not Found' },
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(_to, _from, savedPosition) {
    return savedPosition ?? { top: 0 }
  },
})

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : ''
  document.title = title ? `${title} · Qiu Market` : 'Qiu Market'
})
