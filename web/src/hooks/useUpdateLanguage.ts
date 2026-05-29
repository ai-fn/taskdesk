import { useMutation } from '@tanstack/react-query'
import i18n from '@/i18n/config'
import * as accountApi from '@/api/account'

export function useUpdateLanguage() {
  return useMutation({
    mutationFn: async (lang: string) => {
      // Switch UI immediately (optimistic); localStorage is updated by i18next.
      await i18n.changeLanguage(lang)
      await accountApi.updateLanguage(lang)
    },
    onError: (_err, lang) => {
      // Roll back to previous language on failure.
      const prev = i18n.language === lang ? 'en' : i18n.language
      i18n.changeLanguage(prev)
    },
  })
}
