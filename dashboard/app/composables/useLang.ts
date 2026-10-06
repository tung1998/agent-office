import { vi as uiVi, en as uiEn } from '@nuxt/ui/locale'
import vi, { type MessageKey } from '~/locales/vi'
import en from '~/locales/en'

export type Lang = 'vi' | 'en'

// One copy for the whole app: useLang is called everywhere, also outside a
// component (renderMarkdown on every render, label getters, apiError), where a
// useCookie or computed made per call is never cleaned up (a cookieStore
// listener and a watch each time: the tab's memory grew without end).
const COOKIE = 'office-lang'
let shared: { lang: Ref<Lang>, uiLocale: ComputedRef<typeof uiVi>, dateLocale: ComputedRef<string> } | undefined
function readCookie(): Lang {
  if (typeof document === 'undefined') return 'vi'
  return /(?:^|;\s*)office-lang=en(?:;|$)/.test(document.cookie) ? 'en' : 'vi'
}

export function useLang() {
  if (!shared) {
    const lang = ref<Lang>(readCookie())
    shared = {
      lang,
      uiLocale: computed(() => (lang.value === 'en' ? uiEn : uiVi)),
      dateLocale: computed(() => (lang.value === 'en' ? 'en-US' : 'vi-VN'))
    }
  }
  const { lang, uiLocale, dateLocale } = shared

  function setLang(l: Lang) {
    lang.value = l
    if (typeof document !== 'undefined') document.cookie = `${COOKIE}=${l}; path=/; max-age=31536000; samesite=lax`
  }

  function t(key: MessageKey, params?: Record<string, string | number>): string {
    const locales = lang.value === 'en' ? en : vi
    let text = locales[key] ?? vi[key] ?? key
    if (params) {
      Object.entries(params).forEach(([name, value]) => {
        text = text.replace(new RegExp(`\\{${name}\\}`, 'g'), String(value))
      })
    }
    return text
  }

  return {
    lang,
    setLang,
    t,
    uiLocale,
    dateLocale,
  }
}
