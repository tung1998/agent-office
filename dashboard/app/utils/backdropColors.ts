// Colors of a person's own background (useTheme's Backdrop), worked out in the
// browser: a small swatch of the picture (a few frames of a video), the least
// glass over it that keeps muted text readable (WCAG, ADR-249), and a set of
// theme colors that go with it.
import { contrast, hex, luma, mix, rgb, type ThemeColors } from '~/utils/themes'

const W = 8
const H = 6
const MAX = 96

function load<T extends HTMLImageElement | HTMLVideoElement>(el: T, ev: string): Promise<T> {
  return new Promise((resolve, reject) => {
    el.addEventListener(ev, () => resolve(el), { once: true })
    el.addEventListener('error', () => reject(new Error('cannot read the file')), { once: true })
  })
}

// the picture's colors, as hex: an 8×6 grid of it (and of 3 frames of a
// video), near-duplicates dropped
export async function sampleBackdrop(src: string, kind: 'image' | 'video'): Promise<string[]> {
  const c = document.createElement('canvas')
  c.width = W
  c.height = H
  const x = c.getContext('2d', { willReadFrequently: true })
  if (!x) return []
  const seen = new Set<string>()
  const out: string[] = []
  const grab = (el: CanvasImageSource) => {
    x.drawImage(el, 0, 0, W, H)
    const d = x.getImageData(0, 0, W, H).data
    for (let i = 0; i < d.length; i += 4) {
      const key = [d[i]!, d[i + 1]!, d[i + 2]!].map(v => v >> 4).join(',')
      if (seen.has(key)) continue
      seen.add(key)
      out.push(hex([d[i]! / 255, d[i + 1]! / 255, d[i + 2]! / 255]))
    }
  }
  if (kind === 'image') {
    const img = new Image()
    img.src = src
    grab(await load(img, 'load'))
  } else {
    const v = document.createElement('video')
    v.muted = true
    v.preload = 'auto'
    v.src = src
    await load(v, 'loadeddata')
    for (const at of [0.1, 0.5, 0.9]) {
      v.currentTime = (v.duration || 0) * at
      await load(v, 'seeked')
      grab(v)
    }
  }
  return out.slice(0, MAX)
}

// The least glass (the theme's background color over the picture) for muted
// text to reach 4.5 and dimmed text 3 over every color of it.
export function minGlass(swatch: string[], bg: string, muted: string, dimmed: string): number {
  if (!swatch.length) return 0
  const ok = (a: number) => swatch.every((s) => {
    const under = mix(s, bg, a)
    return contrast(muted, under) >= 4.5 && contrast(dimmed, under) >= 3
  })
  let a = 0.3
  while (a < 0.98 && !ok(a)) a = Math.round((a + 0.01) * 100) / 100
  return a
}

const sat = (c: string) => { const v = rgb(c); return Math.max(...v) - Math.min(...v) }

// Theme colors that go with the picture: light or dark by its brightness, the
// background and text tinted by its average color, the accent its most vivid
// one, pushed until it reads on the background (4.5).
export function suggestColors(swatch: string[]): ThemeColors | undefined {
  if (!swatch.length) return undefined
  const avg = hex([0, 1, 2].map(i => swatch.reduce((n, c) => n + rgb(c)[i]!, 0) / swatch.length))
  const dark = luma(avg) < 0.55
  const bg = dark ? mix(avg, '#000000', 0.82) : mix(avg, '#ffffff', 0.9)
  const text = dark ? mix(avg, '#ffffff', 0.9) : mix(avg, '#000000', 0.85)
  let accent = [...swatch].sort((a, b) => sat(b) - sat(a))[0]!
  if (sat(accent) < 0.15) accent = dark ? '#8b7cf6' : '#5b4bd6' // a grey picture: the office's violet
  for (let i = 0; i < 20 && contrast(accent, bg) < 4.5; i++) accent = mix(accent, dark ? '#ffffff' : '#000000', 0.1)
  const t: ThemeColors = { bg, text, accent }
  return t
}

// A CSS color as hex, whatever form the page has it in (oklch, var(), rgb…).
export function cssHex(color: string): string | undefined {
  const probe = document.createElement('span')
  probe.style.color = color
  probe.style.display = 'none'
  document.documentElement.append(probe)
  const resolved = getComputedStyle(probe).color
  probe.remove()
  const c = document.createElement('canvas')
  c.width = c.height = 1
  const x = c.getContext('2d', { willReadFrequently: true })
  if (!x) return undefined
  x.fillStyle = resolved
  x.fillRect(0, 0, 1, 1)
  const d = x.getImageData(0, 0, 1, 1).data
  return hex([d[0]! / 255, d[1]! / 255, d[2]! / 255])
}
