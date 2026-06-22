import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

import enCommon from './locales/en/common.json'
import enNav from './locales/en/nav.json'
import enLogin from './locales/en/login.json'
import enDashboard from './locales/en/dashboard.json'
import enSites from './locales/en/sites.json'
import enApps from './locales/en/apps.json'
import enSystem from './locales/en/system.json'

import zhCommon from './locales/zh/common.json'
import zhNav from './locales/zh/nav.json'
import zhLogin from './locales/zh/login.json'
import zhDashboard from './locales/zh/dashboard.json'
import zhSites from './locales/zh/sites.json'
import zhApps from './locales/zh/apps.json'
import zhSystem from './locales/zh/system.json'

export const SUPPORTED_LANGUAGES = ['en', 'zh'] as const
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number]
export const DEFAULT_LANGUAGE: SupportedLanguage = 'en'
export const STORAGE_KEY = 'nanoku.lang'

function detectInitialLanguage(): SupportedLanguage {
  if (typeof window === 'undefined') return DEFAULT_LANGUAGE
  const stored = window.localStorage?.getItem(STORAGE_KEY)
  if (stored && (SUPPORTED_LANGUAGES as readonly string[]).includes(stored)) {
    return stored as SupportedLanguage
  }
  const nav = window.navigator?.language.toLowerCase() ?? ''
  if (nav.startsWith('zh')) return 'zh'
  return DEFAULT_LANGUAGE
}

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    lng: detectInitialLanguage(),
    fallbackLng: DEFAULT_LANGUAGE,
    supportedLngs: SUPPORTED_LANGUAGES as unknown as string[],
    ns: [
      'common',
      'nav',
      'login',
      'dashboard',
      'sites',
      'apps',
      'system',
    ],
    defaultNS: 'common',
    interpolation: { escapeValue: false },
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: STORAGE_KEY,
      caches: ['localStorage'],
    },
    resources: {
      en: {
        common: enCommon,
        nav: enNav,
        login: enLogin,
        dashboard: enDashboard,
        sites: enSites,
        apps: enApps,
        system: enSystem,
      },
      zh: {
        common: zhCommon,
        nav: zhNav,
        login: zhLogin,
        dashboard: zhDashboard,
        sites: zhSites,
        apps: zhApps,
        system: zhSystem,
      },
    },
    returnNull: false,
  })

export default i18n
