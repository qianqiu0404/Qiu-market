import {
  panelReadAgeSeconds,
  type PanelReadState,
} from './panel-state'

export type EventTransportState =
  | 'offline'
  | 'connecting'
  | 'live'
  | 'retrying'
  | 'polling'

export type TerminalAvailability = 'LIVE' | 'DEGRADED' | 'OFFLINE'

export interface TerminalHealthInput {
  now: number
  loggedIn: boolean
  matchingStatus: string
  statusRead: PanelReadState
  orderbookRead: PanelReadState
  hasBids: boolean
  hasAsks: boolean
  eventTransport: EventTransportState
  privateDataAt: number
  reconcileComplete: boolean
}

export interface TerminalHealth {
  matchingState: string
  liquidityState: 'active' | 'one-sided' | 'paused' | 'stale' | 'offline'
  transportState: 'websocket' | 'polling' | 'reconnecting' | 'offline'
  dataAgeSeconds: number
  availability: TerminalAvailability
  writesAllowed: boolean
  writeBlockReason: string
}

const WRITE_FRESHNESS_LIMIT_MS = 10_000

function maxKnownAge(...ages: number[]): number {
  const known = ages.filter((age) => age >= 0)
  return known.length ? Math.max(...known) : -1
}

export function deriveTerminalHealth(
  input: TerminalHealthInput,
): TerminalHealth {
  const statusAge = panelReadAgeSeconds(input.statusRead, input.now)
  const orderbookAge = panelReadAgeSeconds(input.orderbookRead, input.now)
  const privateAge = input.privateDataAt > 0
    ? Math.max(0, Math.floor((input.now - input.privateDataAt) / 1000))
    : -1
  const statusFresh = statusAge >= 0 &&
    statusAge <= 10 &&
    input.statusRead.error === ''
  const orderbookFresh = orderbookAge >= 0 &&
    orderbookAge <= 10 &&
    input.orderbookRead.error === ''

  let matchingState = input.matchingStatus
  if (!input.statusRead.lastSuccessAt) matchingState = 'offline'
  else if (!statusFresh) matchingState = 'stale'

  let liquidityState: TerminalHealth['liquidityState']
  if (!input.orderbookRead.lastSuccessAt) liquidityState = 'offline'
  else if (!orderbookFresh) liquidityState = 'stale'
  else if (input.hasBids && input.hasAsks) liquidityState = 'active'
  else if (input.hasBids || input.hasAsks) liquidityState = 'one-sided'
  else liquidityState = 'paused'

  let transportState: TerminalHealth['transportState']
  if (input.eventTransport === 'live') transportState = 'websocket'
  else if (input.eventTransport === 'polling') transportState = 'polling'
  else if (
    input.eventTransport === 'connecting' ||
    input.eventTransport === 'retrying'
  ) {
    transportState = 'reconnecting'
  } else {
    transportState = 'offline'
  }

  const pollingFresh = !input.loggedIn ||
    (input.privateDataAt > 0 &&
      input.now - input.privateDataAt <= WRITE_FRESHNESS_LIMIT_MS)
  const transportReady = transportState === 'websocket' ||
    (transportState === 'polling' && pollingFresh)
  const criticalDataExists = input.statusRead.lastSuccessAt > 0 ||
    input.orderbookRead.lastSuccessAt > 0

  let availability: TerminalAvailability = 'DEGRADED'
  if (!criticalDataExists) {
    availability = 'OFFLINE'
  } else if (
    statusFresh &&
    orderbookFresh &&
    input.matchingStatus === 'ready' &&
    liquidityState === 'active' &&
    transportReady &&
    input.reconcileComplete
  ) {
    availability = 'LIVE'
  }

  let writeBlockReason = ''
  if (!input.loggedIn) writeBlockReason = 'login_required'
  else if (!input.reconcileComplete) writeBlockReason = 'reconcile_pending'
  else if (!input.statusRead.lastSuccessAt) writeBlockReason = 'matching_status_missing'
  else if (!statusFresh) writeBlockReason = 'matching_status_stale'
  else if (input.matchingStatus !== 'ready') {
    writeBlockReason = `matching_${input.matchingStatus || 'not_ready'}`
  } else if (!transportReady) writeBlockReason = 'transport_not_reconciled'

  return {
    matchingState,
    liquidityState,
    transportState,
    dataAgeSeconds: maxKnownAge(
      statusAge,
      orderbookAge,
      input.loggedIn && transportState === 'polling' ? privateAge : -1,
    ),
    availability,
    writesAllowed: writeBlockReason === '',
    writeBlockReason,
  }
}
