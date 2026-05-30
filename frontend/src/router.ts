import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/dashboard' },
  { path: '/dashboard', component: () => import('./views/Dashboard.vue') },
  { path: '/pipeline', component: () => import('./views/Pipeline.vue') },
  { path: '/assets', component: () => import('./views/Assets.vue') },
  { path: '/exchanges', component: () => import('./views/Exchanges.vue') },
  { path: '/symbols', component: () => import('./views/Symbols.vue') },
  { path: '/markets', component: () => import('./views/Markets.vue') },
  { path: '/klines', component: () => import('./views/Klines.vue') },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})
