import { createContext, Fragment, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { isLanguage, LANGUAGE_STORAGE_KEY, readLanguage, translate, translatedMessage, type Language, type TextValues } from './localization'

interface I18nContextValue {
  language: Language
  locale: string
  setLanguage: (language: Language) => void
  t: (message: string | null | undefined, values?: TextValues) => string
}

const I18nContext = createContext<I18nContextValue | null>(null)

function browserLanguage(): Language {
  let storage: Storage | null = null
  try { storage = window.localStorage } catch { /* The selector still works without storage. */ }
  return readLanguage(storage, navigator.languages?.length ? navigator.languages : [navigator.language])
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, updateLanguage] = useState<Language>(browserLanguage)
  const setLanguage = useCallback((next: Language) => {
    if (!isLanguage(next)) return
    updateLanguage(next)
    try { window.localStorage.setItem(LANGUAGE_STORAGE_KEY, next) } catch { /* Keep this tab's choice in memory. */ }
  }, [])
  useEffect(() => {
    document.documentElement.lang = language
    document.title = language === 'zh-CN' ? 'nanotail 管理门户' : 'nanotail portal'
  }, [language])
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === LANGUAGE_STORAGE_KEY || event.key === null) updateLanguage(browserLanguage())
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])
  const t = useCallback((message: string | null | undefined, values?: TextValues) => translate(message, language, values), [language])
  const value = useMemo(() => ({ language, locale: language, setLanguage, t }), [language, setLanguage, t])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n requires I18nProvider')
  return value
}

// Rich messages interpolate React nodes, never HTML. Links and emphasized
// labels keep their semantics and can be reordered by each translation.
export function T({ message, values }: { message: string; values: Record<string, ReactNode> }) {
  const { language } = useI18n()
  return <>{translatedMessage(message, language).split(/\{(\w+)\}/g).map((part, index) =>
    <Fragment key={index}>{index % 2 ? (Object.hasOwn(values, part) ? values[part] : '{' + part + '}') : part}</Fragment>
  )}</>
}
