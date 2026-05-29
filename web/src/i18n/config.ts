import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import en from '../locales/en/translation.json'
import zh from '../locales/zh/translation.json'

export const SUPPORTED_LANGS = ['en', 'zh'] as const
export type SupportedLang = (typeof SUPPORTED_LANGS)[number]

// Maps our locale codes to BCP-47 tags for Intl date/number APIs.
export function toBCP47(lang: string): string {
  const map: Record<string, string> = { en: 'en-US', zh: 'zh-CN' }
  return map[lang] ?? 'en-US'
}

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: { en: { translation: en }, zh: { translation: zh } },
    fallbackLng: 'en',
    supportedLngs: SUPPORTED_LANGS as unknown as string[],
    interpolation: { escapeValue: false },
    detection: {
      // Resolution order: localStorage → navigator
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'lang',
      caches: ['localStorage'],
    },
  })

export default i18n
