import { readonly, ref } from 'vue'

export type Locale = 'en' | 'zh-CN'

export const LOCALE_STORAGE_KEY = 'qiu-market.locale'

const activeLocale = ref<Locale>('en')

export function normalizeLocale(value: string | null | undefined): Locale | null {
  if (!value) return null
  const normalized = value.trim().toLowerCase()
  if (normalized === 'zh' || normalized.startsWith('zh-')) return 'zh-CN'
  if (normalized === 'en' || normalized.startsWith('en-')) return 'en'
  return null
}

export function resolveInitialLocale(
  stored: string | null | undefined,
  browserLanguages: readonly string[] = [],
): Locale {
  const saved = normalizeLocale(stored)
  if (saved) return saved
  for (const language of browserLanguages) {
    const detected = normalizeLocale(language)
    if (detected) return detected
  }
  return 'en'
}

function applyDocumentLocale(locale: Locale): void {
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

export function initializeLocale(): Locale {
  let stored: string | null = null
  try {
    stored = window.localStorage.getItem(LOCALE_STORAGE_KEY)
  } catch {
    // Storage may be disabled. Browser language remains a safe display-only fallback.
  }
  const languages = typeof navigator === 'undefined'
    ? []
    : navigator.languages?.length
      ? navigator.languages
      : [navigator.language]
  activeLocale.value = resolveInitialLocale(stored, languages)
  applyDocumentLocale(activeLocale.value)
  return activeLocale.value
}

export function setLocale(locale: Locale): void {
  activeLocale.value = locale
  applyDocumentLocale(locale)
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  } catch {
    // The selection still applies for this tab when persistence is unavailable.
  }
}

export function useI18n() {
  return {
    locale: readonly(activeLocale),
    setLocale,
  }
}
