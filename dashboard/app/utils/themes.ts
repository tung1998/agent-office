// The dashboard's themes (useTheme). A theme is three colors (bg, text,
// accent) and maybe a font; everything Nuxt UI paints with (its neutral and
// primary scales, --ui-bg, --ui-text…) is derived from them, so a new theme is
// one line here, and a person's own one is the same three colors.
// A wallpaper theme adds a live ASCII scene (app/wallpaper/, loaded only then)
// and the sky behind it. Palettes and scenes from ai-chat-themes by Aohkne
// (MIT): https://github.com/Aohkne/ai-chat-themes

// muted (secondary text: times, names, hints), surface (cards, menus, inputs)
// and border are optional: left out, they follow from bg and text
export interface ThemeColors { bg: string, text: string, accent: string, muted?: string, surface?: string, border?: string }
export const EXTRA_COLORS = ['muted', 'surface', 'border'] as const
export type ExtraColor = typeof EXTRA_COLORS[number]
export interface Theme extends ThemeColors {
  id: string
  font?: string
  // a scene in app/wallpaper/scenes/<id>.js, and the page's sky behind it
  wallpaper?: { sky: string, glass: string }
}
export type ThemeId = string

const MONO = 'ui-monospace, Menlo, \'Cascadia Code\', Consolas, monospace'
const SERIF = 'Georgia, \'Times New Roman\', serif'

const bands = (a: number, sky: string) => {
  const w = (k: number) => `rgba(255, 255, 255, ${+(a * k).toFixed(3)})`
  return `linear-gradient(90deg, ${w(0)} 16%, ${w(1)} 25%, ${w(0)} 35%, ${w(0)} 66%, ${w(0.7)} 77%, ${w(0)} 88%), ${sky}`
}
const live = (id: string, [bg, text, accent]: [string, string, string], sky: string, glass: string): Theme =>
  ({ id, bg, text, accent, wallpaper: { sky, glass } })

// 'default' is Nuxt UI's own look (with the light/dark switch); 'custom' the person's colors
export const COLOR_THEMES: Theme[] = [
  { id: 'sunset-cloud', bg: '#2b2030', text: '#f7e8e2', accent: '#ff8a6b' },
  { id: 'night-sea', bg: '#0f1a2c', text: '#dce6fa', accent: '#5aaeff' },
  { id: 'terminal', bg: '#0a110c', text: '#b9f6c6', accent: '#39e87a', font: MONO },
  { id: 'sakura', bg: '#fff4f6', text: '#4b2a35', accent: '#c23a66' },
  { id: 'old-paper', bg: '#f2e9d6', text: '#3a2f22', accent: '#a5522c', font: SERIF },
  { id: 'mint', bg: '#f3faf6', text: '#1f3a30', accent: '#0b7a5f' }
]

export const WALLPAPER_THEMES: Theme[] = [
  live('moonlit', ['#0d1530', '#dbe2f5', '#d9c98f'], 'radial-gradient(120% 90% at 75% 10%, #1a2a5e, #0d1530 60%, #090f24)', 'rgba(22, 32, 70, .7)'),
  live('fireflies', ['#0b130e', '#e3eedc', '#d8e86a'], bands(0.03, 'radial-gradient(120% 80% at 50% 110%, #1b3522, #0c1710 60%, #070c09)'), 'rgba(14, 26, 18, .72)'),
  live('neon-rain', ['#0c1020', '#e3e8f6', '#ff5fa2'], 'linear-gradient(#05070f, #0b1024 40%, #1e1838 75%, #34203f)', 'rgba(12, 16, 32, .7)'),
  live('star-voyage', ['#0d0a1f', '#e6ecff', '#ff6fb5'], 'radial-gradient(110% 90% at 85% 20%, #2a1a52, #120d2e 50%, #08061a)', 'rgba(16, 12, 40, .7)'),
  live('snow-cabin', ['#0e1628', '#e6edf8', '#ffb85c'], 'linear-gradient(#060b18, #101c38 55%, #1c2a4a)', 'rgba(14, 22, 40, .7)'),
  live('lantern-night', ['#0b1424', '#efe6d2', '#f2a541'], 'linear-gradient(#070c1c, #24305e 29%, #0e1a30 30%, #08111f 60%, #050a14)', 'rgba(11, 20, 36, .7)'),
  live('sakura-dusk', ['#231a2e', '#f6e9f0', '#f29bb8'], 'linear-gradient(#17112b, #2a1c44 40%, #523058 72%, #8c4a5e)', 'rgba(35, 26, 46, .7)'),
  live('jellyfish', ['#0a1830', '#dfe7fb', '#a58cf0'], bands(0.07, 'linear-gradient(#174784, #0c2b58 50%, #071a3a)'), 'rgba(12, 28, 58, .7)'),
  live('koi-pond', ['#11332d', '#e4f2ea', '#ef8a4a'], bands(0.12, 'linear-gradient(175deg, #4aa08c, #2f8070 50%, #1f6156)'), 'rgba(17, 51, 45, .72)'),
  live('honu-lagoon', ['#0f3a44', '#e3f6f2', '#5fd0b8'], bands(0.14, 'linear-gradient(#35a6f0, #1d7ad8 45%, #1650b2)'), 'rgba(15, 58, 68, .72)'),
  live('cloud-drift', ['#d8ecfa', '#1d3447', '#1f5f99'], bands(0.22, 'linear-gradient(#4fa3e8, #86c3f0 55%, #b6dbf6)'), 'rgba(255, 255, 255, .78)')
]

export const DEFAULT_CUSTOM: ThemeColors = { bg: '#1b1d24', text: '#e4e6ee', accent: '#8b7cf6' }

export const isHex = (c: unknown): c is string => typeof c === 'string' && /^#[0-9a-f]{6}$/i.test(c)
export const rgb = (c: string) => [1, 3, 5].map(i => parseInt(c.slice(i, i + 2), 16) / 255) as [number, number, number]
export const hex = (c: number[]) => '#' + c.map(v => Math.round(Math.max(0, Math.min(1, v)) * 255).toString(16).padStart(2, '0')).join('')
export const mix = (a: string, b: string, k: number) => { const x = rgb(a), y = rgb(b); return hex(x.map((v, i) => v + (y[i]! - v) * k)) }
export const luma = (c: string) => { const [r, g, b] = rgb(c); return 0.2126 * r + 0.7152 * g + 0.0722 * b }
export const isDark = (t: ThemeColors) => luma(t.bg) < 0.5
// WCAG contrast ratio (1–21): body text wants 4.5, large text and icons 3
const lum = (c: string) => { const [r, g, b] = rgb(c).map(v => v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4); return 0.2126 * r! + 0.7152 * g! + 0.0722 * b! }
export const contrast = (a: string, b: string) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05) }
// The text on an accent fill (solid buttons, the person's chat bubble) and on
// the inverted fill (the theme's text color): white or a near-black of the
// theme, whichever reads better on both.
export function onAccent(t: ThemeColors): string {
  const darkInk = luma(t.bg) < luma(t.text) ? t.bg : mix(t.text, '#000000', 0.6)
  const score = (c: string) => Math.min(contrast(c, t.accent), contrast(c, t.text))
  return score('#ffffff') >= score(darkInk) ? '#ffffff' : darkInk
}

export function findTheme(id: string, custom?: ThemeColors): Theme | undefined {
  if (id === 'custom') {
    const c = custom && isHex(custom.bg) && isHex(custom.text) && isHex(custom.accent) ? custom : DEFAULT_CUSTOM
    const t: Theme = { id, bg: c.bg, text: c.text, accent: c.accent }
    for (const k of EXTRA_COLORS) if (isHex(c[k])) t[k] = c[k]
    return t
  }
  return COLOR_THEMES.find(t => t.id === id) ?? WALLPAPER_THEMES.find(t => t.id === id)
}

const SHADES = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950] as const
// how much of the text color each neutral shade carries over the background:
// the light scale runs from the page (50) to ink (950), the dark one back
const INK_LIGHT = [0.03, 0.07, 0.14, 0.26, 0.56, 0.7, 0.78, 0.86, 0.92, 0.96, 1]
const INK_DARK = [1, 0.95, 0.88, 0.76, 0.6, 0.45, 0.32, 0.2, 0.1, 0, -0.25]
// the accent toward white (light shades) and black (dark ones)
const ACCENT = [0.9, 0.8, 0.62, 0.42, 0.2, 0, -0.16, -0.32, -0.48, -0.62, -0.74]

// Keep the accent scale and solid-button text coherent when just the accent changes.
export function primaryVariables(accent: string, darkText = '#18181b'): Record<string, string> {
  const vars: Record<string, string> = {
    '--ui-primary': accent,
    '--ui-text-inverted': contrast('#ffffff', accent) >= contrast(darkText, accent) ? '#ffffff' : darkText
  }
  SHADES.forEach((shade, i) => {
    const a = ACCENT[i]!
    vars[`--ui-color-primary-${shade}`] = a >= 0 ? mix(accent, '#ffffff', a) : mix(accent, '#000000', -a)
  })
  return vars
}

// What muted, surface and border are when the theme leaves them out: Nuxt
// UI's neutral shades (dark: muted 400, cards 800, borders 800; light: 500,
// 100, 200), from the theme's ink scale
export function autoColors(t: ThemeColors): Record<ExtraColor, string> {
  const dark = isDark(t)
  const ink = (i: number) => { const k = (dark ? INK_DARK : INK_LIGHT)[i]!; return k < 0 ? mix(t.bg, '#000000', -k) : mix(t.bg, t.text, k) }
  return dark ? { muted: ink(4), surface: ink(8), border: ink(8) } : { muted: ink(5), surface: ink(1), border: ink(2) }
}

// The glass over a wallpaper's sky, at least as opaque as its own and as much
// more as muted text (--ui-text-muted) needs for 4.5 and dimmed text for 3
// over every color of the sky (WCAG, ADR-249): its color and that opacity
function readableGlass(w: { sky: string, glass: string }, muted: string, dimmed: string): { rgb: string, a: number } | undefined {
  const m = w.glass.match(/rgba\((\d+),\s*(\d+),\s*(\d+),\s*([\d.]+)\)/)
  if (!m) return undefined
  const rgb = `${m[1]}, ${m[2]}, ${m[3]}`
  const stops = w.sky.match(/#[0-9a-f]{6}/gi) ?? []
  let a = Number(m[4])
  if (!stops.length) return { rgb, a }
  const g = hex([m[1], m[2], m[3]].map(v => Number(v) / 255))
  const ok = (a: number) => stops.every((s) => {
    const under = mix(s, g, a)
    return contrast(muted, under) >= 4.6 && contrast(dimmed, under) >= 3.1
  })
  while (a < 0.95 && !ok(a)) a = Math.round((a + 0.01) * 100) / 100
  return { rgb, a }
}

// The CSS a theme sets on <html data-theme>: unlayered, so it wins over Nuxt
// UI's theme layer; the light/dark class is set to match (useTheme).
// glass: the person's own opacity of the panes over a wallpaper (0–1); left
// out, the readable one.
export function themeCss(t: Theme, glass?: number): string {
  const dark = isDark(t)
  const vars = themeVars(t)
  let css = `html[data-theme="${t.id}"]{${Object.entries(vars).map(([k, v]) => `${k}:${v}`).join(';')}}`
  if (t.wallpaper) {
    // the scene shows through: the page has no background of its own; the
    // sidebar and the page (PageShell) are glass, as opaque as the sky needs
    // for muted text to read, or as the person set it (cards, menus and
    // dialogs keep theirs)
    const r = readableGlass(t.wallpaper, vars['--ui-text-muted'] ?? vars[`--ui-color-neutral-${dark ? 400 : 500}`]!, vars['--ui-text-dimmed'] ?? vars[`--ui-color-neutral-${dark ? 500 : 400}`]!)
    const g = r ? `rgba(${r.rgb}, ${glass ?? r.a})` : t.wallpaper.glass
    css += `html[data-theme="${t.id}"]{background:${t.wallpaper.sky} fixed;--wall-glass:${g}}`
      + `html[data-theme="${t.id}"] body{background:transparent}`
      + `html[data-theme="${t.id}"] .office-sidebar,html[data-theme="${t.id}"] .office-main{background:var(--wall-glass)}`
  }
  return css
}

// the least glass over a wallpaper for its muted text to read (0: none)
export function wallGlassFloor(t: Theme): number {
  if (!t.wallpaper) return 0
  const dark = isDark(t)
  const vars = themeVars(t)
  return readableGlass(t.wallpaper, vars['--ui-text-muted'] ?? vars[`--ui-color-neutral-${dark ? 400 : 500}`]!, vars['--ui-text-dimmed'] ?? vars[`--ui-color-neutral-${dark ? 500 : 400}`]!)?.a ?? 0
}

function themeVars(t: Theme): Record<string, string> {
  const dark = isDark(t)
  const ink = (k: number) => k < 0 ? mix(t.bg, '#000000', -k) : mix(t.bg, t.text, k)
  const vars: Record<string, string> = {}
  SHADES.forEach((s, i) => {
    vars[`--ui-color-neutral-${s}`] = ink((dark ? INK_DARK : INK_LIGHT)[i]!)
    const a = ACCENT[i]!
    vars[`--ui-color-primary-${s}`] = a >= 0 ? mix(t.accent, '#ffffff', a) : mix(t.accent, '#000000', -a)
  })
  if (t.muted) Object.assign(vars, { '--ui-text-muted': t.muted, '--ui-text-dimmed': mix(t.muted, t.bg, 0.3) })
  if (t.surface) Object.assign(vars, { '--ui-bg-elevated': t.surface, '--ui-bg-muted': mix(t.bg, t.surface, 0.5), '--ui-bg-accented': mix(t.surface, t.text, 0.1) })
  if (t.border) Object.assign(vars, { '--ui-border': t.border, '--ui-border-muted': t.border, '--ui-border-accented': mix(t.border, t.text, 0.2) })
  Object.assign(vars, {
    '--ui-primary': t.accent,
    '--ui-bg': t.bg,
    '--ui-text-highlighted': t.text,
    '--ui-bg-inverted': t.text,
    '--ui-text-inverted': onAccent(t)
  })
  if (t.font) vars['--font-sans'] = t.font
  return vars
}
