<script setup lang="ts">
import PageHeader from '../components/PageHeader.vue'
</script>

<template>
  <section>
    <PageHeader title="Page not found" subtitle="The route you opened does not exist" />
    <div class="card card-pad not-found">
      <div class="code mono">404</div>
      <p class="message">This page does not exist or may have been moved.</p>
      <RouterLink to="/dashboard" class="btn btn--accent">Back to Dashboard</RouterLink>
    </div>
  </section>
</template>

<style scoped>
.not-found {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 64px 24px;
  text-align: center;
}

.code {
  font-size: 48px;
  font-weight: 700;
  color: var(--accent);
}

.message {
  margin: 0;
  color: var(--text-2);
}
</style>
