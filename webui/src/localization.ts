import { zhCN } from './locales/zh-CN.ts'

export type Language = 'en' | 'zh-CN'
export const LANGUAGE_STORAGE_KEY = 'nanotail_language'
export type TextValues = Record<string, string | number>

export function isLanguage(value: unknown): value is Language {
  return value === 'en' || value === 'zh-CN'
}

export function chooseLanguage(saved: string | null, preferred: readonly string[] = []): Language {
  if (isLanguage(saved)) return saved
  for (const language of preferred) {
    if (/^zh(?:-|$)/i.test(language)) return 'zh-CN'
    if (/^en(?:-|$)/i.test(language)) return 'en'
  }
  return 'en'
}

export function readLanguage(storage: Pick<Storage, 'getItem'> | null, preferred: readonly string[]): Language {
  let saved: string | null = null
  try { saved = storage?.getItem(LANGUAGE_STORAGE_KEY) ?? null } catch { /* Private browsing may block storage. */ }
  return chooseLanguage(saved, preferred)
}

export function translatedMessage(message: string, language: Language): string {
  if (language === 'en') return message
  if (Object.hasOwn(zhCN, message)) return zhCN[message]
  // GraphQL may return several known diagnostics. Preserve unfamiliar server
  // messages verbatim: never translate device names, addresses, or credentials.
  if (message.includes('\n')) return message.split('\n').map(line => translatedMessage(line, language)).join('\n')
  return message
}

export function translate(message: string | null | undefined, language: Language, values: TextValues = {}): string {
  return translatedMessage(message ?? '', language).replace(/\{(\w+)\}/g, (placeholder, key: string) =>
    Object.hasOwn(values, key) ? String(values[key]) : placeholder)
}
