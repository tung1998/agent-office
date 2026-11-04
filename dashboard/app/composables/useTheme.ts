// The dashboard's theme (utils/themes.ts), kept with the account
// (/api/me/prefs/theme) so it follows it to any browser, and in the browser
// too, so the page has it on the first paint (plugins/theme.client.ts).
// A theme sets <html data-theme> plus one <style>, and the light/dark class
// its background calls for; 'default' removes both and gives the light/dark
// switch back. A wallpaper theme's scene is ThemeWallpaper's (lazy).
import { findTheme, isDark, themeCss, type Theme, type ThemeColors } from '~/utils/themes'

export interface WallLook { strength: number, speed: number, cell: number, still: boolean }
export interface SavedTheme { id: string, custom?: ThemeColors, wall?: Partial<WallLook> }
export const WALL_LOOK: WallLook = { strength: 0.45, speed: 1, cell: 18, still: false }
const PREF_URL = '/api/me/prefs/theme'
const KEY = 'office.theme'
const STYLE_ID = 'office-theme'

export function readSavedTheme(): SavedTheme {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? 'null')
    if (v && typeof v.id === 'string') return v
  } catch { /* a broken copy: the default */ }
  return { id: 'default' }
}

// Puts a theme on the page (also used before Vue mounts, by the plugin).
export function paintTheme(s: SavedTheme): Theme | undefined {
  const root = document.documentElement
  const t = s.id === 'default' ? undefined : findTheme(s.id, s.custom)
  let style = document.getElementById(STYLE_ID)
  if (!t) {
    delete root.dataset.theme
    style?.remove()
    return undefined
  }
  if (!style) {
    style = document.createElement('style')
    style.id = STYLE_ID
  }
  style.textContent = themeCss(t)
  document.head.append(style) // last: after Nuxt UI's sheets
  root.dataset.theme = t.id
  const dark = isDark(t)
  root.classList.toggle('dark', dark)
  root.classList.toggle('light', !dark)
  return t
}

export function useTheme() {
  const { user } = useAuth()
  const colorMode = useColorMode()
  const saved = useState<SavedTheme>('theme', () => import.meta.client ? readSavedTheme() : { id: 'default' })
  const loadedFor = useState('theme:user', () => '')
  const theme = computed(() => saved.value.id === 'default' ? undefined : findTheme(saved.value.id, saved.value.custom))
  const look = computed<WallLook>(() => ({ ...WALL_LOOK, ...saved.value.wall }))

  function apply(s: SavedTheme) {
    const t = paintTheme(s)
    // a theme picks light or dark by its background; the color-mode module
    // must agree or it puts its own class back
    if (t) colorMode.preference = isDark(t) ? 'dark' : 'light'
  }

  async function load() {
    const id = user.value?.id ?? ''
    if (!id || loadedFor.value === id) return
    loadedFor.value = id
    try {
      const v = (await $fetch<{ value: SavedTheme | null }>(PREF_URL)).value
      const next = v && typeof v.id === 'string' ? v : { id: 'default' }
      if (JSON.stringify(next) !== JSON.stringify(saved.value)) {
        // the default switches back to the person's own light/dark choice
        if (next.id === 'default' && saved.value.id !== 'default') colorMode.preference = 'system'
        saved.value = next
        localStorage.setItem(KEY, JSON.stringify(next))
        apply(next)
      }
    } catch {
      loadedFor.value = '' // try again on the next page
    }
  }

  async function save(next: SavedTheme) {
    const before = saved.value
    if (next.id === 'default' && before.id !== 'default') colorMode.preference = 'system'
    saved.value = next
    localStorage.setItem(KEY, JSON.stringify(next))
    apply(next)
    try {
      await $fetch(PREF_URL, { method: 'PUT', body: { value: next } })
    } catch (e) {
      saved.value = before
      localStorage.setItem(KEY, JSON.stringify(before))
      apply(before)
      throw e
    }
  }

  const setTheme = (id: string) => save({ ...saved.value, id })
  const setCustom = (custom: ThemeColors) => save({ ...saved.value, id: 'custom', custom })
  const setLook = (o: Partial<WallLook>) => save({ ...saved.value, wall: { ...saved.value.wall, ...o } })

  return { saved, theme, look, load, setTheme, setCustom, setLook }
}
