/** Column definition for DataTable. */
export interface TableColumn<T = unknown> {
  key: string
  label: string
  align?: 'left' | 'right' | 'center'
  sortable?: boolean
  width?: string
  /** Custom accessor for sorting; defaults to row[key]. */
  sortValue?: (row: T) => number | string
}
