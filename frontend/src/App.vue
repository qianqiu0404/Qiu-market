<template>
  <div class="app-container">
    <nav class="sidebar">
      <div class="brand">
        <span class="brand-icon">Q</span>
        <div>
          <div class="brand-text">Qiu Market</div>
          <div class="brand-sub">Real-time Crypto Dashboard</div>
        </div>
      </div>
      <div class="nav-section-label">Overview</div>
      <router-link to="/dashboard" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg>
        Dashboard
      </router-link>
      <div class="nav-section-label">Data</div>
      <router-link to="/markets" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12V7H5a2 2 0 0 1 0-4h14v4"/><path d="M3 5v14a2 2 0 0 0 2 2h16v-5"/><path d="M18 12a2 2 0 0 0 0 4h4v-4Z"/></svg>
        Markets
      </router-link>
      <router-link to="/klines" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 3v18h18"/><path d="m7 16 4-8 4 4 4-8"/></svg>
        Klines
      </router-link>
      <router-link to="/pipeline" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20z"/><path d="M12 6v6l4 2"/></svg>
        Pipeline
      </router-link>
      <div class="nav-section-label">System</div>
      <router-link to="/assets" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg>
        Assets
      </router-link>
      <router-link to="/exchanges" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/></svg>
        Exchanges
      </router-link>
      <router-link to="/symbols" class="nav-link">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>
        Symbols
      </router-link>
    </nav>
    <main class="content">
      <router-view></router-view>
    </main>
  </div>
</template>

<style>
.app-container {
  display: flex;
  min-height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.sidebar {
  width: 220px;
  background: var(--bg-secondary);
  display: flex;
  flex-direction: column;
  padding: 20px 12px;
  border-right: 1px solid var(--border);
  position: sticky;
  top: 0;
  height: 100vh;
  overflow-y: auto;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 8px 24px;
  margin-bottom: 8px;
  border-bottom: 1px solid var(--border);
}
.brand-icon {
  width: 36px; height: 36px;
  display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, var(--accent-cyan), var(--accent-blue));
  border-radius: 10px;
  font-size: 0.95rem; font-weight: 800;
  color: white;
  flex-shrink: 0;
}
.brand-text {
  font-size: 1.05rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--text-primary);
  line-height: 1.2;
}
.brand-sub {
  font-size: 0.62rem;
  color: var(--text-muted);
  letter-spacing: 0.02em;
  margin-top: 1px;
}
.nav-section-label {
  padding: 16px 8px 6px;
  font-size: 0.65rem;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  font-weight: 600;
}
.nav-link {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  text-decoration: none;
  color: var(--text-secondary);
  border-radius: var(--radius-sm);
  margin-bottom: 2px;
  font-size: 0.85rem;
  font-weight: 500;
  transition: var(--transition);
}
.nav-link:hover {
  background: rgba(59,130,246,0.08);
  color: var(--text-primary);
}
.nav-link svg { opacity: 0.6; flex-shrink: 0; }
.nav-link.router-link-active {
  background: rgba(59,130,246,0.12);
  color: var(--accent-blue);
}
.nav-link.router-link-active svg { opacity: 1; color: var(--accent-blue); }
.content {
  flex: 1;
  padding: 32px 36px;
  overflow-y: auto;
  max-height: 100vh;
}
</style>
