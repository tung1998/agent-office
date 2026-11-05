// The custom theme's advanced colors: a background and a text color of its
// own for one region of the page (the classes app.config.ts and the layout
// put on them), and the links' color. Left out, a region keeps the theme's;
// what is not set is worked out from what is (muted text, raised surfaces).
// Idea from Nguyễn Nam Long's per-component colors (tung1998/agent-office).
import { isHex } from './themes'

export const REGIONS = ['sidebar', 'main', 'card', 'input', 'popup'] as const
export type Region = typeof REGIONS[number]
export interface RegionColor { bg?: string, text?: string }
export type RegionColors = Partial<Record<Region, RegionColor>> & { link?: string }

// a saved value may come from an older browser or the API: known hex only
export function readRegionColors(v: unknown): RegionColors {
  const out: RegionColors = {}
  if (!v || typeof v !== 'object') return out
  const r = v as RegionColors
  if (isHex(r.link)) out.link = r.link
  for (const k of REGIONS) {
    const c: RegionColor = {}
    if (isHex(r[k]?.bg)) c.bg = r[k]!.bg
    if (isHex(r[k]?.text)) c.text = r[k]!.text
    if (c.bg || c.text) out[k] = c
  }
  return out
}

export const hasRegionColors = (r: RegionColors) => Object.keys(readRegionColors(r)).length > 0

// The CSS under scope (html[data-theme="custom"]): each region sets the
// theme's variables for what is inside it
export function regionCss(scope: string, v: unknown): string {
  const r = readRegionColors(v)
  let css = ''
  for (const k of REGIONS) {
    const c = r[k]
    if (!c) continue
    const decl: string[] = []
    if (c.bg) {
      decl.push(`--ui-bg:${c.bg}`, `--ui-bg-muted:color-mix(in srgb,${c.bg} 95%,var(--ui-text))`,
        `--ui-bg-elevated:color-mix(in srgb,${c.bg} 90%,var(--ui-text))`, `--ui-bg-accented:color-mix(in srgb,${c.bg} 82%,var(--ui-text))`,
        `background-color:${c.bg}`)
    }
    if (c.text) {
      decl.push(`--ui-text:${c.text}`, `--ui-text-highlighted:${c.text}`, `--ui-text-toned:${c.text}`,
        `--ui-text-muted:color-mix(in srgb,${c.text} 70%,var(--ui-bg))`, `--ui-text-dimmed:color-mix(in srgb,${c.text} 50%,var(--ui-bg))`,
        `color:${c.text}`)
    }
    css += `${scope} .office-${k}{${decl.join(';')}}`
  }
  if (r.link) css += `${scope} a:not([data-slot]):not([role="button"]){color:${r.link}}`
  return css
}
