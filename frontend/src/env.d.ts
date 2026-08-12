/// <reference types="vite/client" />

declare const __QIU_MARKET_RELEASE_COMMIT__: string

interface ImportMetaEnv {
  readonly VITE_TRADING_WS_ORIGIN?: string
  readonly VITE_TRADING_EVENT_MODE?: 'websocket' | 'polling'
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, any>
  export default component
}
