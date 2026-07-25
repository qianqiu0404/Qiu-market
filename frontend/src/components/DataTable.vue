<script setup lang="ts" generic="T extends object">
import { computed, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import type { TableColumn } from './table'

const props = withDefaults(
  defineProps<{
    columns: TableColumn<T>[]
    rows: T[]
    /** Property used as :key for each row. */
    rowKey: keyof T & string
    loading?: boolean
    searchable?: boolean
    searchPlaceholder?: string
    emptyTitle?: string
    emptyMessage?: string
    /** Server-driven pagination: rows are already the current page slice. */
    serverMode?: boolean
    total?: number
    rowClickable?: (row: T) => boolean
  }>(),
  {
    loading: false,
    searchable: false,
    searchPlaceholder: 'Search…',
    emptyTitle: 'No results',
    emptyMessage: 'Nothing matched the current filter.',
    serverMode: false,
    total: 0,
  },
)
const emit = defineEmits<{ rowClick: [row: T] }>()

const page = defineModel<number>('page', { default: 1 })
const pageSize = defineModel<number>('pageSize', { default: 20 })
const query = defineModel<string>('query', { default: '' })
const sortKey = defineModel<string | null>('sortKey', { default: null })
const sortDir = defineModel<'asc' | 'desc'>('sortDir', { default: 'asc' })

const pageSizeOptions = [10, 20, 50]

function rowText(row: T): string {
  return Object.values(row as Record<string, unknown>)
    .map((v) => (v == null ? '' : String(v)))
    .join(' ')
    .toLowerCase()
}

const filtered = computed<T[]>(() => {
  if (props.serverMode) return props.rows
  const q = query.value.trim().toLowerCase()
  if (!q) return props.rows
  return props.rows.filter((row) => rowText(row).includes(q))
})

function sortValueOf(row: T, col: TableColumn<T>): number | string {
  if (col.sortValue) return col.sortValue(row)
  const value = (row as Record<string, unknown>)[col.key]
  if (typeof value === 'number') return value
  return value == null ? '' : String(value)
}

const sorted = computed<T[]>(() => {
  if (props.serverMode) return filtered.value
  const key = sortKey.value
  if (!key) return filtered.value
  const col = props.columns.find((c) => c.key === key)
  if (!col) return filtered.value
  const dir = sortDir.value === 'asc' ? 1 : -1
  return [...filtered.value].sort((a, b) => {
    const va = sortValueOf(a, col)
    const vb = sortValueOf(b, col)
    if (typeof va === 'number' && typeof vb === 'number') return (va - vb) * dir
    return String(va).localeCompare(String(vb)) * dir
  })
})

const totalCount = computed(() => (props.serverMode ? props.total : sorted.value.length))
const pageCount = computed(() => Math.max(1, Math.ceil(totalCount.value / pageSize.value)))

const visibleRows = computed<T[]>(() => {
  if (props.serverMode) return sorted.value
  const start = (page.value - 1) * pageSize.value
  return sorted.value.slice(start, start + pageSize.value)
})

const rangeStart = computed(() => (totalCount.value === 0 ? 0 : (page.value - 1) * pageSize.value + 1))
const rangeEnd = computed(() =>
  props.serverMode
    ? Math.min(totalCount.value, (page.value - 1) * pageSize.value + props.rows.length)
    : Math.min(totalCount.value, page.value * pageSize.value),
)

function toggleSort(col: TableColumn<T>): void {
  if (!col.sortable) return
  if (props.serverMode) {
    if (sortKey.value === col.key) {
      sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
    } else {
      sortKey.value = col.key
      sortDir.value = 'desc'
    }
    page.value = 1
    return
  }
  if (sortKey.value === col.key) {
    if (sortDir.value === 'asc') {
      sortDir.value = 'desc'
    } else {
      sortKey.value = null
      sortDir.value = 'asc'
    }
  } else {
    sortKey.value = col.key
    sortDir.value = 'asc'
  }
}

function prevPage(): void {
  if (page.value > 1) page.value -= 1
}

function nextPage(): void {
  if (page.value < pageCount.value) page.value += 1
}

watch(query, () => {
  page.value = 1
})

watch(pageSize, () => {
  page.value = 1
})

function cellValue(row: T, col: TableColumn<T>): unknown {
  return (row as Record<string, unknown>)[col.key]
}

function handleRowClick(row: T): void {
  if (!props.rowClickable?.(row)) return
  emit('rowClick', row)
}
</script>

<template>
  <div class="data-table card">
    <div v-if="searchable" class="table-toolbar">
      <div class="table-search">
        <AppIcon name="search" :size="15" />
        <input v-model="query" class="table-search-input" type="search" :placeholder="searchPlaceholder" />
      </div>
    </div>

    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th
              v-for="col in columns"
              :key="col.key"
              :class="[{ sortable: col.sortable, sorted: sortKey === col.key }, `align-${col.align ?? 'left'}`]"
              :style="col.width ? { width: col.width } : undefined"
              @click="toggleSort(col)"
            >
              <span class="th-inner">
                {{ col.label }}
                <svg
                  v-if="col.sortable"
                  class="sort-arrow"
                  :class="{ desc: sortKey === col.key && sortDir === 'desc' }"
                  width="10"
                  height="10"
                  viewBox="0 0 10 10"
                  aria-hidden="true"
                >
                  <path d="M5 1.5 9 7H1z" fill="currentColor" />
                </svg>
              </span>
            </th>
          </tr>
        </thead>
        <tbody v-if="loading">
          <tr v-for="i in 6" :key="i" class="skeleton-tr">
            <td :colspan="columns.length">
              <div class="shimmer" style="height: 14px"></div>
            </td>
          </tr>
        </tbody>
        <tbody v-else-if="visibleRows.length > 0">
          <tr
            v-for="(row, index) in visibleRows"
            :key="String(row[rowKey] ?? index)"
            :class="{ 'row-clickable': rowClickable?.(row) }"
            @click="handleRowClick(row)"
          >
            <td
              v-for="col in columns"
              :key="col.key"
              :class="`align-${col.align ?? 'left'}`"
            >
              <slot :name="`cell-${col.key}`" :row="row" :value="cellValue(row, col)" :index="index">
                {{ cellValue(row, col) ?? '—' }}
              </slot>
            </td>
          </tr>
        </tbody>
        <tbody v-else>
          <tr>
            <td :colspan="columns.length" class="empty-cell">
              <EmptyState :title="emptyTitle" :message="emptyMessage" />
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="table-footer">
      <span class="table-range num">{{ rangeStart }}–{{ rangeEnd }} of {{ totalCount }}</span>
      <div class="table-pager">
        <label class="page-size-label">
          Rows
          <select v-model.number="pageSize" class="input page-size-select">
            <option v-for="opt in pageSizeOptions" :key="opt" :value="opt">{{ opt }}</option>
          </select>
        </label>
        <button type="button" class="btn pager-btn" :disabled="page <= 1" @click="prevPage">
          <AppIcon name="chevron-left" :size="15" />
        </button>
        <span class="num pager-page">{{ page }} / {{ pageCount }}</span>
        <button type="button" class="btn pager-btn" :disabled="page >= pageCount" @click="nextPage">
          <AppIcon name="chevron-right" :size="15" />
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.data-table {
  overflow: hidden;
}

.table-toolbar {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 12px;
}

.table-search {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-3);
  background: var(--bg-panel-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0 12px;
  flex: 1;
  max-width: 340px;
}

.table-search:focus-within {
  border-color: var(--accent);
}

.table-search-input {
  flex: 1;
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--text-1);
  font: inherit;
  font-size: 13px;
  padding: 8px 0;
  outline: none;
}

.table-search-input::placeholder {
  color: var(--text-3);
}

.table-scroll {
  overflow-x: auto;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  min-width: 560px;
}

.row-clickable {
  cursor: pointer;
}

.row-clickable:hover {
  background: var(--accent-soft);
}

th {
  text-align: left;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-3);
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
  user-select: none;
}

th.sortable {
  cursor: pointer;
}

th.sortable:hover {
  color: var(--text-1);
}

.th-inner {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.sort-arrow {
  opacity: 0.25;
  transition: transform 0.15s ease, opacity 0.15s ease;
}

th.sorted .sort-arrow {
  opacity: 1;
  color: var(--accent);
}

.sort-arrow.desc {
  transform: rotate(180deg);
}

td {
  padding: 11px 16px;
  border-bottom: 1px solid var(--border);
  color: var(--text-1);
  white-space: nowrap;
}

tbody tr:last-child td {
  border-bottom: 0;
}

tbody tr:hover td {
  background: #f8fbff;
}

.align-right {
  text-align: right;
}

.align-center {
  text-align: center;
}

.align-right .th-inner {
  flex-direction: row-reverse;
}

.empty-cell {
  padding: 0;
}

.table-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 16px;
  color: var(--text-3);
  font-size: 12px;
}

.table-pager {
  display: flex;
  align-items: center;
  gap: 8px;
}

.page-size-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.page-size-select {
  padding: 4px 8px;
  font-size: 12px;
}

.pager-btn {
  padding: 5px 8px;
}

.pager-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.pager-page {
  color: var(--text-2);
  min-width: 48px;
  text-align: center;
}
</style>
