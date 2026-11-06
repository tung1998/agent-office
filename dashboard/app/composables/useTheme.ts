// The dashboard's theme (utils/themes.ts), kept with the account
// (/api/me/prefs/theme) so it follows it to any browser, and in the browser
// too, so the page has it on the first paint (plugins/theme.client.ts).
// A theme sets <html data-theme> plus one <style>, and the light/dark class
// its background calls for; 'default' removes both and gives the light/dark
// switch back. A wallpaper theme's scene is ThemeWallpaper's (lazy).
import { findTheme, isDark, themeCss, type Theme, type ThemeColors } from '~/utils/themes'
import { cssHex, minGlass, sampleBackdrop } from '~/utils/backdropColors'
import { regionCss, type RegionColors } from '~/utils/regionColors'
import { isPackSrc } from '~/utils/backgroundPacks'

// blur: px, 0 = sharp
export interface WallLook { strength: number, speed: number, cell: number, still: boolean, blur: number }
// a background of the person's own (/api/me/background), on any theme: the
// file's kind, its version (the cache key), how opaque the panes over it are,
// a swatch of its colors (utils/backdropColors) to keep text readable on it,
// how strongly it shows (0–1, 1 by default) and how blurred it is (px, 0 =
// sharp); src: an image of a pack the dashboard ships (utils/backgroundPacks.ts)
// instead of the person's file
export interface Backdrop { kind: 'image' | 'video', v: number, src?: string, glass?: number, swatch?: string[], strength?: number, blur?: number }
export const BLUR_MAX = 30
export const CLEAR_MAX = 0.9
// wallGlass: how opaque the panes over a wallpaper theme are, the person's own;
// regions: the custom theme's advanced colors (utils/regionColors.ts);
// clear: how see-through the cards are over a background or wallpaper (0–0.9);
// tweak: the person's colors over a wallpaper theme, which keeps its scene
export interface SavedTheme { id: string, custom?: ThemeColors, tweak?: ThemeColors, regions?: RegionColors, wall?: Partial<WallLook>, wallGlass?: number, bg?: Backdrop, clear?: number }
export const WALL_LOOK: WallLook = { strength: 0.45, speed: 1, cell: 18, still: false, blur: 0 }
const CLEAR_ID = 'office-clear'
const REGIONS_ID = 'office-regions'
const PREF_URL = '/api/me/prefs/theme'
const KEY = 'office.theme'
const STYLE_ID = 'office-theme'
const BACKDROP_ID = 'office-backdrop'
export const BG_URL = '/api/me/background'
export const BG_MAX = 25 << 20
export const BG_GLASS = 0.82
export const backdropSrc = (b: Pick<Backdrop, 'v' | 'src'>) => isPackSrc(b.src) ? b.src : `${BG_URL}?v=${b.v}`
// a file of the person's background library (/api/me/backgrounds)
export interface BgItem { mime: string, size: number, version: number }
export const bgKind = (mime: string): Backdrop['kind'] => mime.startsWith('video/') ? 'video' : 'image'
// a background saved before the library had its version in nanoseconds: the
// server reads it as its millisecond, and so does this
export const bgVersion = (v: number) => v > 1e15 ? Math.floor(v / 1e6) : v

// The least glass over a background for the current theme's muted text to
// read on every color of it (0 when its colors are not known)
export function backdropFloor(b: Backdrop): number {
  if (!b.swatch?.length) return 0
  const bg = cssHex('var(--ui-bg)'), muted = cssHex('var(--ui-text-muted)'), dimmed = cssHex('var(--ui-text-dimmed)')
  return bg && muted && dimmed ? minGlass(b.swatch, bg, muted, dimmed) : 0
}
// the glass in use: the person's (lighter than the floor if they like), else
// the floor or more
export const backdropGlass = (b: Backdrop, floor = backdropFloor(b)) => b.glass ?? (floor ? Math.max(floor, 0.6) : BG_GLASS)
// the lightest glass the slider goes to
export const GLASS_MIN = 0.2

// The page over a background: no fill of its own, the sidebar and the page
// panes are the theme's background color, see-through by glass.
export function paintBackdrop(b: Backdrop | undefined) {
  const root = document.documentElement
  let style = document.getElementById(BACKDROP_ID)
  if (!b) {
    delete root.dataset.backdrop
    style?.remove()
    return
  }
  if (!style) {
    style = document.createElement('style')
    style.id = BACKDROP_ID
  }
  const g = Math.round(backdropGlass(b) * 100)
  style.textContent = 'html[data-backdrop] body{background:transparent}'
    + `html[data-backdrop] body .office-sidebar,html[data-backdrop] body .office-main{background:color-mix(in srgb,var(--ui-bg) ${g}%,transparent)}`
  document.head.append(style)
  root.dataset.backdrop = b.kind
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
  const t = paintColors(s)
  paintBackdrop(s.bg) // after: its glass depends on the theme's colors
  paintClear(s.bg || t?.wallpaper ? s.clear : undefined)
  paintRegions(s.regions)
  return t
}
// the advanced colors by region, on any theme (last: they win over it)
function paintRegions(regions: RegionColors | undefined) {
  const css = regionCss('html', regions)
  let style = document.getElementById(REGIONS_ID)
  if (!css) {
    style?.remove()
    return
  }
  if (!style) {
    style = document.createElement('style')
    style.id = REGIONS_ID
  }
  style.textContent = css
  document.head.append(style)
}
// The cards over a background or wallpaper: see-through by clear (none: solid)
function paintClear(clear: number | undefined) {
  let style = document.getElementById(CLEAR_ID)
  if (!clear) {
    style?.remove()
    return
  }
  if (!style) {
    style = document.createElement('style')
    style.id = CLEAR_ID
  }
  style.textContent = `html .office-card{background-color:color-mix(in srgb,var(--ui-bg) ${Math.round((1 - Math.min(clear, CLEAR_MAX)) * 100)}%,transparent)}`
  document.head.append(style)
}
function paintColors(s: SavedTheme): Theme | undefined {
  const root = document.documentElement
  const t = s.id === 'default' ? undefined : findTheme(s.id, s.custom, s.tweak)
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
  style.textContent = themeCss(t, s.wallGlass)
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
  const library = useState<BgItem[]>('theme:backgrounds', () => [])
  const theme = computed(() => saved.value.id === 'default' ? undefined : findTheme(saved.value.id, saved.value.custom, saved.value.tweak))
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

  // a new theme starts with its own readable glass
  const setTheme = (id: string) => save({ ...saved.value, id, wallGlass: undefined, tweak: undefined })
  // own colors: over a wallpaper theme they tweak it (the scene stays), else
  // they are the custom theme
  const setCustom = (custom: ThemeColors) => findTheme(saved.value.id)?.wallpaper
    ? save({ ...saved.value, tweak: custom })
    : save({ ...saved.value, id: 'custom', custom })
  const setLook = (o: Partial<WallLook>) => save({ ...saved.value, wall: { ...saved.value.wall, ...o } })

  async function loadLibrary() {
    library.value = await $fetch<BgItem[]>(`${BG_URL}s`)
  }
  // a file of the library becomes the background
  async function pickBackground(item: BgItem) {
    if (saved.value.bg && bgVersion(saved.value.bg.v) === item.version) return
    const bg: Backdrop = { kind: bgKind(item.mime), v: item.version }
    // its colors, for the glass's floor and the "colors from the picture" button
    bg.swatch = await sampleBackdrop(backdropSrc(bg), bg.kind).catch(() => undefined)
    await save({ ...saved.value, bg })
  }
  // an image of a pack the dashboard ships becomes the background
  async function pickPackImage(src: string) {
    if (saved.value.bg?.src === src) return
    const bg: Backdrop = { kind: 'image', v: 0, src }
    bg.swatch = await sampleBackdrop(src, 'image').catch(() => undefined)
    await save({ ...saved.value, bg })
  }
  // the file goes into the library, then becomes the background
  async function setBackground(file: File) {
    const data = await new Promise<string>((resolve, reject) => {
      const r = new FileReader()
      r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ''))
      r.onerror = () => reject(r.error)
      r.readAsDataURL(file)
    })
    const item = await $fetch<BgItem>(BG_URL, { method: 'PUT', body: { data } })
    library.value = [item, ...library.value]
    await pickBackground(item)
  }
  // no background; the file stays in the library
  async function clearBackground() {
    const { bg: _, ...rest } = saved.value
    await save(rest)
  }
  async function deleteBackground(item: BgItem) {
    if (saved.value.bg && bgVersion(saved.value.bg.v) === item.version) await clearBackground()
    await $fetch(BG_URL, { method: 'DELETE', query: { v: item.version } })
    library.value = library.value.filter(i => i.version !== item.version)
  }
  // a background from before swatches: its colors, once
  async function sampleSaved() {
    const b = saved.value.bg
    if (!b || b.swatch) return
    const swatch = await sampleBackdrop(backdropSrc(b), b.kind).catch(() => undefined)
    if (swatch?.length && saved.value.bg?.v === b.v && saved.value.bg?.src === b.src) await save({ ...saved.value, bg: { ...saved.value.bg, swatch } })
  }
  const setGlass = (glass: number) => saved.value.bg && save({ ...saved.value, bg: { ...saved.value.bg, glass } })
  // dragging shows the glass at once; setGlass keeps it when let go
  function previewGlass(glass: number) {
    if (!saved.value.bg) return
    saved.value = { ...saved.value, bg: { ...saved.value.bg, glass } }
    paintBackdrop(saved.value.bg)
  }

  const setRegions = (regions: RegionColors) => save({ ...saved.value, regions })
  // the background's strength and blur: dragging shows them at once,
  // keepBg saves them when let go
  function previewBg(o: Pick<Backdrop, 'strength' | 'blur'>) {
    if (!saved.value.bg) return
    saved.value = { ...saved.value, bg: { ...saved.value.bg, ...o } }
  }
  const keepBg = () => saved.value.bg && save({ ...saved.value })
  // the wallpaper's look the same way (setLook saves a change at once)
  function previewWall(o: Partial<WallLook>) {
    saved.value = { ...saved.value, wall: { ...saved.value.wall, ...o } }
  }
  const keepWall = () => save({ ...saved.value })
  function previewClear(clear: number) {
    saved.value = { ...saved.value, clear }
    paintClear(clear)
  }
  const keepClear = () => save({ ...saved.value })

  const setWallGlass = (v: number | undefined) => save({ ...saved.value, wallGlass: v })
  function previewWallGlass(v: number) {
    saved.value = { ...saved.value, wallGlass: v }
    paintColors(saved.value)
  }

  return { setWallGlass, previewWallGlass, saved, theme, look, library, load, setTheme, setCustom, setLook, loadLibrary, pickBackground, setBackground, clearBackground, deleteBackground, sampleSaved, setGlass, previewGlass, previewBg, keepBg, previewWall, keepWall, previewClear, keepClear, setRegions, pickPackImage }
}
