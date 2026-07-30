export interface PanelReadState {
  lastAttemptAt: number
  lastSuccessAt: number
  error: string
}

export type PanelReadAvailability =
  | 'loading'
  | 'current'
  | 'last-good'
  | 'unavailable'

export function createPanelReadState(): PanelReadState {
  return {
    lastAttemptAt: 0,
    lastSuccessAt: 0,
    error: '',
  }
}

export function beginPanelRead(
  current: PanelReadState,
  attemptedAt: number,
): PanelReadState {
  return {
    ...current,
    lastAttemptAt: attemptedAt,
  }
}

export function completePanelRead(
  current: PanelReadState,
  succeededAt: number,
): PanelReadState {
  return {
    ...current,
    lastAttemptAt: succeededAt,
    lastSuccessAt: succeededAt,
    error: '',
  }
}

export function failPanelRead(
  current: PanelReadState,
  error: string,
  failedAt: number,
): PanelReadState {
  return {
    ...current,
    lastAttemptAt: failedAt,
    error,
  }
}

export function panelReadAgeSeconds(
  current: PanelReadState,
  now: number,
): number {
  if (!current.lastSuccessAt) return -1
  return Math.max(0, Math.floor((now - current.lastSuccessAt) / 1000))
}

export function panelReadAvailability(
  current: PanelReadState,
): PanelReadAvailability {
  if (!current.lastSuccessAt) {
    return current.error ? 'unavailable' : 'loading'
  }
  return current.error ? 'last-good' : 'current'
}
