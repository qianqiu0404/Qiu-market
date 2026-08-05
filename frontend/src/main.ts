import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { initializeLocale } from './i18n'
import './style.css'

initializeLocale()
createApp(App).use(router).mount('#app')
