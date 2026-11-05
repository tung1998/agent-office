// The dashboard's theme (utils/themes.ts), kept with the account
// (/api/me/prefs/theme) so it follows it to any browser, and in the browser
// too, so the page has it on the first paint (plugins/theme.client.ts).
// A theme sets <html data-theme> plus one <style>, and the light/dark class
// its background calls for; 'default' removes both and gives the light/dark
// switch back. A wallpaper theme's scene is ThemeWallpaper's (lazy).
import { bounded, readMediaBackground, type MediaBackground } from '~/utils/backgrounds'
import { findTheme, isDark, themeCss, type Theme, type ThemeColors } from '~/utils/themes'

export interface WallLook { strength: number, speed: number, cell: number, still: boolean, transparency: number, blur: number }
export interface SavedTheme { id: string, custom?: ThemeColors, wall?: Partial<WallLook>, background?: MediaBackground }
export const WALL_LOOK: WallLook = { strength: 0.45, speed: 1, cell: 18, still: false, transparency: 0.3, blur: 0 }
const PREF_URL = '/api/me/prefs/theme'
const KEY = 'office.theme'
const STYLE_ID = 'office-theme'

export function wallLook(w?: Partial<WallLook>): WallLook {
  return {
    strength: bounded(w?.strength, WALL_LOOK.strength, 0, 1),
    speed: bounded(w?.speed, WALL_LOOK.speed, 0.1, 4),
    cell: bounded(w?.cell, WALL_LOOK.cell, 10, 30),
    still: w?.still === true,
    transparency: bounded(w?.transparency, WALL_LOOK.transparency, 0, 0.9),
    blur: bounded(w?.blur, WALL_LOOK.blur, 0, 30)
  }
}

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
  if (!t) delete root.dataset.theme
  const background = readMediaBackground(s.background)
  const hasWallpaper = !!background || !!t?.wallpaper
  root.toggleAttribute('data-wallpaper', hasWallpaper)
  if (!t && !hasWallpaper) {
    style?.remove()
    return undefined
  }
  if (!style) {
    style = document.createElement('style')
    style.id = STYLE_ID
  }
  const look = wallLook(s.wall)
  style.textContent = (t ? themeCss(t) : '') + (hasWallpaper ? `
    html[data-wallpaper]{--office-surface:${t?.bg ?? 'var(--color-white)'};--office-glass:${(1 - look.transparency).toFixed(2)};background:${!background && t?.wallpaper ? t.wallpaper.sky : "var(--office-surface)"}}
    html.dark[data-wallpaper]{--office-surface:${t?.bg ?? 'var(--color-neutral-900)'}}
    html[data-wallpaper] body{background:transparent;--ui-bg:color-mix(in srgb,var(--office-surface) calc(var(--office-glass)*100%),transparent)}
    html[data-wallpaper] .office-sidebar{background:var(--ui-bg)}
    html[data-wallpaper] :is([role="dialog"],[role="menu"],[role="listbox"]){--ui-bg:var(--office-surface)}
  ` : '')
  document.head.append(style) // last: after Nuxt UI's sheets
  if (!t) return undefined
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
  const saving = useState('theme:saving', () => false)
  const preview = useState<Partial<WallLook> | undefined>('theme:preview', () => undefined)
  const look = computed<WallLook>(() => wallLook({ ...saved.value.wall, ...preview.value }))
  const background = computed(() => readMediaBackground(saved.value.background))

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
    if (saving.value) return
    saving.value = true
    const before = saved.value
    const previousColorMode = colorMode.preference
    preview.value = undefined
    try {
      if (next.id === 'default' && before.id !== 'default') colorMode.preference = 'system'
      saved.value = next
      localStorage.setItem(KEY, JSON.stringify(next))
      apply(next)
      await $fetch(PREF_URL, { method: 'PUT', body: { value: next } })
    } catch (e) {
      saved.value = before
      colorMode.preference = previousColorMode
      try { localStorage.setItem(KEY, JSON.stringify(before)) } catch { /* storage may be full */ }
      apply(before)
      throw e
    } finally { saving.value = false }
    const oldSource = before.background?.src
    if (oldSource?.startsWith('local:') && oldSource !== next.background?.src) {
      // A cleanup error must not roll back an already persisted preference.
      try { await (await import('~/utils/localBackground')).removeBackgroundFile(oldSource) } catch { /* best effort */ }
    }
  }

  function previewLook(o: Partial<WallLook>) {
    // Preview never mutates the saved snapshot: failed saves restore it exactly.
    preview.value = { ...preview.value, ...o }
    paintTheme({ ...saved.value, wall: { ...saved.value.wall, ...preview.value } })
  }
  const setBackground = (background?: MediaBackground) => save({ ...saved.value, background })
  const setTheme = (id: string) => save({ ...saved.value, id, background: findTheme(id)?.wallpaper ? undefined : saved.value.background })
  const setCustom = (custom: ThemeColors) => save({ ...saved.value, id: 'custom', custom })
  const setLook = (o: Partial<WallLook>) => save({ ...saved.value, wall: { ...saved.value.wall, ...o } })

  return { saved, theme, look, background, saving, load, setTheme, setCustom, setLook, setBackground, previewLook }
}
