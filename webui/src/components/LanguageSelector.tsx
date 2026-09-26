import { Languages } from 'lucide-react'
import { useI18n } from '../i18n'
import { isLanguage } from '../localization'

export default function LanguageSelector() {
  const { language, setLanguage, t } = useI18n()
  return <label className="language-selector">
    <Languages size={17} aria-hidden="true" />
    <select aria-label={t('Language')} value={language} onChange={event => {
      if (isLanguage(event.target.value)) setLanguage(event.target.value)
    }}>
      <option value="en" lang="en">English</option>
      <option value="zh-CN" lang="zh-CN">简体中文</option>
    </select>
  </label>
}
