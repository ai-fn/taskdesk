import { useTranslation } from 'react-i18next'
import { SUPPORTED_LANGS, type SupportedLang } from '@/i18n/config'
import { useUpdateLanguage } from '@/hooks/useUpdateLanguage'

const langLabels: Record<SupportedLang, string> = {
  en: 'English',
  zh: '中文',
}

export function LanguageSwitcher() {
  const { i18n } = useTranslation()
  const update = useUpdateLanguage()

  const current = (SUPPORTED_LANGS as readonly string[]).includes(i18n.language)
    ? (i18n.language as SupportedLang)
    : 'en'

  return (
    <div className="flex gap-1 rounded-lg border border-gray-200 dark:border-gray-700 p-0.5 w-fit">
      {SUPPORTED_LANGS.map((lang) => (
        <button
          key={lang}
          disabled={update.isPending}
          onClick={() => { if (lang !== current) update.mutate(lang) }}
          className={`rounded-md px-3 py-1 text-sm font-medium transition-colors ${
            lang === current
              ? 'bg-indigo-600 text-white'
              : 'text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800'
          }`}
        >
          {langLabels[lang]}
        </button>
      ))}
    </div>
  )
}
