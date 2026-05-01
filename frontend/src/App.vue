<template>
  <div class="app-container">
    <nav class="sidebar">
      <div class="brand">S78 Market</div>
      <router-link to="/dashboard">Dashboard</router-link>
      <router-link to="/pipeline">Pipeline</router-link>
      <router-link to="/assets">Assets</router-link>
      <router-link to="/exchanges">Exchanges</router-link>
      <router-link to="/symbols">Symbols</router-link>
      <router-link to="/markets">Markets</router-link>
      <router-link to="/klines">Klines</router-link>
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
  background: #0f172a;
  color: #f8fafc;
}
.sidebar {
  width: 240px;
  background: #1e293b;
  display: flex;
  flex-direction: column;
  padding: 20px;
  border-right: 1px solid #334155;
}
.brand {
  font-size: 1.5rem;
  font-weight: bold;
  margin-bottom: 2rem;
  color: #38bdf8;
}
.sidebar a {
  padding: 12px 16px;
  text-decoration: none;
  color: #94a3b8;
  border-radius: 8px;
  margin-bottom: 4px;
}
.sidebar a.router-link-active {
  background: #38bdf8;
  color: white;
}
.content {
  flex: 1;
  padding: 40px;
  overflow-y: auto;
}
</style>
