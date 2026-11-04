// The scene kit: what the scenes in scenes/ draw with. From ai-chat-themes by
// Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes (src/core.js).
// Only loaded with a wallpaper theme: nothing here is in the main bundle.

const rgb = c => [1, 3, 5].map(i => parseInt(c.slice(i, i + 2), 16) / 255)
const hex = c => '#' + c.map(v => Math.round(v * 255).toString(16).padStart(2, '0')).join('')
const mix = (a, b, k) => a.map((v, i) => v + (b[i] - v) * k)

export const GLYPHS = 'St%;:X', LOOSE = ".:',o"
export const hash = (a, b, c = 0) => {
  let h = Math.imul(a, 374761393) ^ Math.imul(b, 668265263) ^ Math.imul(c, 1442695041)
  h = Math.imul(h ^ (h >>> 13), 1274126177)
  return ((h ^ (h >>> 16)) >>> 0) / 4294967296
}
export const glyph = (set, x, y, t) => set[Math.floor(hash(x, y, Math.floor(t * 1.2 + hash(y, x) * 7)) * set.length)]

export const ell = (x, y, cx, cy, rx, ry) => ((x - cx) / rx) ** 2 + ((y - cy) / ry) ** 2
export const drift = (cols, f, t, v, w) => { const span = cols + w * 2; return ((f * span + t * v) % span) - w }
export const near = (x, y, fg, a = 0.3, odds = 0.5) => (hash(x, y, 3) < odds ? { fg, a } : undefined)
export const twinkle = (x, y, t, fg, odds) => {
  const s = hash(x, y, 9)
  if (s < odds) return { ch: s < odds / 3 ? '+' : '.', fg, a: 0.2 + 0.5 * (0.5 + 0.5 * Math.sin(t * (1 + s * 200) + s * 1000)) }
}
export const rising = (x, y, t, v, odds) => hash(x, Math.floor(y + t * v), 11) < odds

export const sketch = (w, h, parts) => {
  const g = Array.from({ length: h }, () => Array(w).fill('.'))
  for (const [r, c, s] of parts) [...s].forEach((ch, i) => { if (ch !== ' ' && g[r]?.[c + i]) g[r][c + i] = ch })
  return g.map(r => r.join(''))
}
export const shape = (w, h, at) => Array.from({ length: h }, (_, y) => Array.from({ length: w }, (_, x) => at(x, y)).join(''))
export const flip = art => art.map(r => [...r].reverse().join(''))
export const frame = (frames, t, rate, phase = 0) => frames[Math.floor(t * rate + phase) % frames.length]
export const shade = (c, k) => hex(mix(rgb(c), k > 0 ? [1, 1, 1] : [0, 0, 0], Math.abs(k)))
export const tint = (bg, fg, alphas) => Object.fromEntries(alphas.map((a, i) => [String(i + 1), { bg, fg, a }]))
const halos = new WeakMap()
export const haloOf = (art) => {
  if (!halos.has(art)) {
    const on = (x, y) => (art[y]?.[x] ?? '.') !== '.', out = []
    for (let y = -1; y <= art.length; y++)
      for (let x = -1; x <= art[0].length; x++)
        if (!on(x, y) && [-1, 0, 1].some(i => [-1, 0, 1].some(j => on(x + i, y + j))) && hash(x + 50, y + 50, 5) < 0.45) out.push([x, y])
    halos.set(art, out)
  }
  return halos.get(art)
}

// the sidebar's width in cells: scenes keep their sprites clear of it (set by the engine)
export let SIDE = 0
export const setSide = (cells) => { SIDE = cells }
