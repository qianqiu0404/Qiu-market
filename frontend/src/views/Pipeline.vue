<template>
  <div class="page">
    <div class="header">
      <h1>Data Pipeline</h1>
    </div>
    <div class="pipeline-container">
      <div class="step">
        <div class="icon">🌐</div>
        <div class="label">Exchanges</div>
        <div class="desc">Binance, OKX...</div>
      </div>
      <div class="arrow">➜</div>
      <div class="step">
        <div class="icon">🕷️</div>
        <div class="label">Crawler</div>
        <div class="desc">Fetching Data</div>
      </div>
      <div class="arrow">➜</div>
      <div class="step">
        <div class="icon">💾</div>
        <div class="label">Redis/DB</div>
        <div class="desc">Storage</div>
      </div>
      <div class="arrow">➜</div>
      <div class="step">
        <div class="icon">⚙️</div>
        <div class="label">Worker</div>
        <div class="desc">Processing</div>
      </div>
      <div class="arrow">➜</div>
      <div class="step active">
        <div class="icon">🚀</div>
        <div class="label">REST/RPC</div>
        <div class="desc">API Service</div>
      </div>
      <div class="arrow">➜</div>
      <div class="step active">
        <div class="icon">💻</div>
        <div class="label">Frontend</div>
        <div class="desc">Dashboard MVP</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pipeline-container { display: flex; align-items: center; justify-content: space-between; margin-top: 60px; padding: 40px; background: var(--bg-card); border: 1px solid var(--border); border-radius: 16px; box-shadow: var(--shadow); }
.step { flex: 1; text-align: center; padding: 20px; border: 2px solid var(--border); border-radius: 12px; background: #f8fafc; }
.step.active { border-color: var(--accent-blue); box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.08); }
.icon { font-size: 2.5rem; margin-bottom: 12px; }
.label { font-weight: bold; margin-bottom: 4px; }
.desc { font-size: 0.75rem; color: var(--text-muted); }
.arrow { font-size: 1.5rem; color: var(--text-muted); margin: 0 10px; }
</style>
