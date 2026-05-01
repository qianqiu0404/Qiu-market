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
.pipeline-container { display: flex; align-items: center; justify-content: space-between; margin-top: 60px; padding: 40px; background: #1e293b; border-radius: 16px; }
.step { flex: 1; text-align: center; padding: 20px; border: 2px solid #334155; border-radius: 12px; background: #0f172a; }
.step.active { border-color: #38bdf8; box-shadow: 0 0 15px rgba(56, 189, 248, 0.3); }
.icon { font-size: 2.5rem; margin-bottom: 12px; }
.label { font-weight: bold; margin-bottom: 4px; }
.desc { font-size: 0.75rem; color: #94a3b8; }
.arrow { font-size: 1.5rem; color: #475569; margin: 0 10px; }
</style>
