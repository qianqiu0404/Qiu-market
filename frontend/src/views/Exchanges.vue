<script setup lang="ts">
import { computed, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import AssetLogo from '../components/AssetLogo.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorState from '../components/ErrorState.vue'
import AppIcon from '../components/AppIcon.vue'
import { usePolling } from '../composables/usePolling'
import { getExchanges } from '../api/market'

const exchanges = usePolling(() => getExchanges(1, 200), { interval: 60_000 })

const query = ref('')

const filtered = computed(() => {
  const list = exchanges.data.value?.items ?? []
  const q = query.value.trim().toLowerCase()
  if (!q) return list
  return list.filter((e) => e.name.toLowerCase().includes(q))
})

const freshness = computed(() =>
  exchanges.error.value ? ('offline' as const) : ('live' as const),
)
</script>

<template>
  <section>
    <PageHeader
      title="Exchanges"
      subtitle="Connected exchange sources"
      :freshness="freshness"
    />

    <div class="toolbar">
      <div class="search-box">
        <AppIcon name="search" :size="15" />
        <input v-model="query" type="search" placeholder="Search exchanges…" class="search-input" />
      </div>
    </div>

    <SkeletonRows v-if="exchanges.loading.value" variant="cards" :rows="8" />
    <ErrorState
      v-else-if="exchanges.error.value && !exchanges.data.value"
      :message="exchanges.error.value"
      @retry="exchanges.refresh"
    />
    <EmptyState
      v-else-if="filtered.length === 0"
      title="No exchanges found"
      message="No exchange matched your search."
    />
    <div v-else class="card-grid">
      <div v-for="exchange in filtered" :key="exchange.guid" class="card card-pad exchange-card">
        <AssetLogo :src="exchange.logo" :name="exchange.name" :size="40" />
        <div class="exchange-info">
          <span class="exchange-name">{{ exchange.name }}</span>
          <span class="exchange-guid mono" :title="exchange.guid">{{ exchange.guid }}</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.toolbar {
  margin-bottom: 16px;
}

.search-box {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-3);
  background: var(--bg-panel-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0 12px;
  max-width: 340px;
}

.search-box:focus-within {
  border-color: var(--accent);
}

.search-input {
  flex: 1;
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--text-1);
  font: inherit;
  font-size: 13px;
  padding: 9px 0;
  outline: none;
}

.search-input::placeholder {
  color: var(--text-3);
}

.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 14px;
}

.exchange-card {
  display: flex;
  align-items: center;
  gap: 12px;
}

.exchange-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.exchange-name {
  font-weight: 600;
}

.exchange-guid {
  font-size: 11px;
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
