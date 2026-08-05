<script setup lang="ts">
import { computed, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import DataTable from '../components/DataTable.vue'
import ErrorState from '../components/ErrorState.vue'
import { usePolling } from '../composables/usePolling'
import { getSymbols, type SymbolInfo } from '../api/market'
import { truncateMiddle } from '../utils/format'
import type { TableColumn } from '../components/table'

const page = ref(1)
const pageSize = ref(20)

const symbols = usePolling(() => getSymbols(page.value, pageSize.value), { interval: 60_000 })

const items = computed<SymbolInfo[]>(() => symbols.data.value?.items ?? [])
const total = computed(() => symbols.data.value?.total ?? 0)

const freshness = computed(() =>
  symbols.error.value ? ('offline' as const) : ('live' as const),
)

const columns: TableColumn<SymbolInfo>[] = [
  { key: 'symbol_name', label: 'Symbol', sortable: true },
  { key: 'base_asset', label: 'Base Asset', sortable: true },
  { key: 'quote_asset', label: 'Quote Asset', sortable: true },
  { key: 'guid', label: 'GUID' },
]
</script>

<template>
  <section>
    <PageHeader
      title="Symbols"
      subtitle="Tracked trading pairs"
      :freshness="freshness"
    />

    <ErrorState
      v-if="symbols.error.value && items.length === 0"
      :message="symbols.error.value"
      @retry="symbols.refresh"
    />
    <DataTable
      v-else
      v-model:page="page"
      v-model:page-size="pageSize"
      :columns="columns"
      :rows="items"
      row-key="guid"
      :loading="symbols.loading.value"
      searchable
      search-placeholder="Search symbols…"
      server-mode
      :total="total"
      empty-title="No symbols"
      empty-message="No symbol matched your search."
    >
      <template #cell-symbol_name="{ row }">
        <span class="symbol-name">{{ row.symbol_name }}</span>
      </template>
      <template #cell-guid="{ row }">
        <span class="mono guid" :title="row.guid">{{ truncateMiddle(row.guid) }}</span>
      </template>
    </DataTable>
  </section>
</template>

<style scoped>
.symbol-name {
  font-weight: 600;
}

.guid {
  color: var(--text-3);
}
</style>
