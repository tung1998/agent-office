import { isHex, primaryVariables } from './themes.ts'

export const COLOR_GROUPS = ['sidebar', 'header', 'panel', 'card', 'input', 'popup'] as const
export type ColorGroup = typeof COLOR_GROUPS[number]
export type SurfaceColors = Partial<Record<'bg' | 'text', string>>
export interface ComponentColors {
  surfaces?: Partial<Record<ColorGroup, SurfaceColors>>
  border?: string
  link?: string
  accent?: string
}

// Preferences may come from an older browser or the API: emit only known hex values.
export function readComponentColors(value: unknown): ComponentColors {
  const result: ComponentColors = {}
  if (!value || typeof value !== 'object') return result
  const c = value as ComponentColors
  for (const key of ['border', 'link', 'accent'] as const) {
    if (isHex(c[key])) result[key] = c[key]
  }
  for (const group of COLOR_GROUPS) {
    const surface: SurfaceColors = {}
    for (const key of ['bg', 'text'] as const) {
      if (isHex(c.surfaces?.[group]?.[key])) surface[key] = c.surfaces![group]![key]
    }
    if (Object.keys(surface).length) (result.surfaces ??= {})[group] = surface
  }
  return result
}

export function componentColorsCss(value: unknown): string {
  const c = readComponentColors(value)
  const root = 'html[data-component-colors]'
  let css = ''
  if (c.accent) css += `${root}{${Object.entries(primaryVariables(c.accent)).map(([key, value]) => `${key}:${value}`).join(';')}}`
  if (c.border) css += `${root}{--ui-border:${c.border};--ui-border-muted:${c.border};--ui-border-accented:${c.border}}`
  if (c.link) css += `${root} a:not([data-slot]):not([role="button"]){color:${c.link}}`
  const tokens = ['bg', 'bg-muted', 'bg-elevated', 'text', 'text-highlighted', 'text-toned', 'text-muted', 'text-dimmed']
  const defaults = `${root}{${tokens.map(key => `--office-base-${key}:var(--ui-${key})`).join(';')}}`
    + `${root} :is(${COLOR_GROUPS.map(group => `.office-${group}`).join(',')}){${tokens.map(key => `--ui-${key}:var(--office-base-${key})`).join(';')};color:var(--ui-text)}`
    + `html[data-component-colors][data-wallpaper] :is(.office-sidebar,.office-header,.office-panel,.office-card){--ui-bg:color-mix(in srgb,var(--office-base-bg) calc(var(--office-glass)*100%),transparent)}`
  for (const group of COLOR_GROUPS) {
    const surface = c.surfaces?.[group]
    if (!surface) continue
    const selector = `${root} .office-${group}`
    if (surface.bg) {
      // Popups and fields stay solid. Page surfaces still respect the wallpaper transparency.
      css += `${selector}{--ui-bg:${surface.bg};--ui-bg-muted:color-mix(in srgb,${surface.bg} 95%,var(--ui-text));--ui-bg-elevated:color-mix(in srgb,${surface.bg} 90%,var(--ui-text));background-color:${surface.bg}}`
      if (['sidebar', 'header', 'panel', 'card'].includes(group)) {
        css += `html[data-component-colors][data-wallpaper] .office-${group}{--ui-bg:color-mix(in srgb,${surface.bg} calc(var(--office-glass)*100%),transparent);background-color:var(--ui-bg)}`
      }
    }
    if (surface.text) css += `${selector}{--ui-text:${surface.text};--ui-text-highlighted:${surface.text};--ui-text-toned:${surface.text};--ui-text-muted:${surface.text};--ui-text-dimmed:${surface.text};color:${surface.text}}`
  }
  return css ? defaults + css : ''
}
