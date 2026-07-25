<script setup lang="ts">
import { computed, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import AssetLogo from '../components/AssetLogo.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorState from '../components/ErrorState.vue'
import AppIcon from '../components/AppIcon.vue'
import { usePolling } from '../composables/usePolling'
import { getSupportAssets } from '../api/market'

const assets = usePolling(getSupportAssets, { interval: 60_000 })

const query = ref('')

const filtered = computed(() => {
  const list = assets.data.value ?? []
  const q = query.value.trim().toLowerCase()
  if (!q) return list
  return list.filter(
    (a) =>
      a.asset_name.toLowerCase().includes(q) || a.asset_symbol.toLowerCase().includes(q),
  )
})

const freshness = computed(() =>
  assets.error.value ? ('offline' as const) : ('live' as const),
)
</script>

<template>
  <section>
    <PageHeader
      title="Assets"
      subtitle="Supported base assets"
      :freshness="freshness"
    />

    <div class="toolbar">
      <div class="search-box">
        <AppIcon name="search" :size="15" />
        <input v-model="query" type="search" placeholder="Search assets…" class="search-input" />
      </div>
    </div>

    <SkeletonRows v-if="assets.loading.value" variant="cards" :rows="8" />
    <ErrorState
      v-else-if="assets.error.value && !assets.data.value"
      :message="assets.error.value"
      @retry="assets.refresh"
    />
    <EmptyState
      v-else-if="filtered.length === 0"
      title="No assets found"
      message="No asset matched your search."
    />
    <div v-else class="card-grid">
      <div v-for="asset in filtered" :key="asset.guid" class="card card-pad asset-card">
        <AssetLogo :src="asset.asset_logo" :name="asset.asset_symbol || asset.asset_name" :size="40" />
        <div class="asset-info">
          <span class="asset-symbol">{{ asset.asset_symbol }}</span>
          <span class="asset-name">{{ asset.asset_name }}</span>
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
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 14px;
}

.asset-card {
  display: flex;
  align-items: center;
  gap: 12px;
}

.asset-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.asset-symbol {
  font-weight: 600;
}

.asset-name {
  font-size: 12px;
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
