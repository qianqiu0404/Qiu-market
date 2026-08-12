export interface RestorePresentation {
  loading: boolean
  refreshing: boolean
  failure: string
  refreshError: string
}

export function applyLateDashboardRestore(
  networkSettled: boolean,
  networkError: string,
): RestorePresentation {
  return {
    loading: false,
    refreshing: !networkSettled,
    failure: '',
    refreshError: networkSettled ? networkError : '',
  }
}
