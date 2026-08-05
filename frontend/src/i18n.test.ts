import { beforeEach, describe, expect, it } from 'vitest'
import {
  initializeLocale,
  LOCALE_STORAGE_KEY,
  normalizeLocale,
  resolveInitialLocale,
  setLocale,
  tradeMessageKeys,
} from './i18n'

describe('locale selection', () => {
  beforeEach(() => window.localStorage.clear())

  it('normalizes supported browser and persisted locale forms', () => {
    expect(normalizeLocale('zh-Hans-CN')).toBe('zh-CN')
    expect(normalizeLocale('en-US')).toBe('en')
    expect(normalizeLocale('ja-JP')).toBeNull()
  })

  it('prefers a persisted choice over browser languages', () => {
    expect(resolveInitialLocale('en', ['zh-CN'])).toBe('en')
    expect(resolveInitialLocale('zh-CN', ['en-US'])).toBe('zh-CN')
  })

  it('uses the first supported browser language and otherwise English', () => {
    expect(resolveInitialLocale(null, ['ja-JP', 'zh-Hans'])).toBe('zh-CN')
    expect(resolveInitialLocale(null, ['fr-FR'])).toBe('en')
  })

  it('persists an explicit choice and restores it on initialization', () => {
    setLocale('zh-CN')
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')

    expect(initializeLocale()).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
  })

  it('keeps the Trade Product V1 English and Chinese key sets identical', () => {
    expect(tradeMessageKeys('zh-CN')).toEqual(tradeMessageKeys('en'))
    expect(tradeMessageKeys('en').length).toBeGreaterThan(100)
  })
})
