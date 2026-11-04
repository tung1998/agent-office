// Draws a scene (scenes/<id>.js) on a fixed canvas behind the dashboard: a
// grid of glyph cells plus sprites. From ai-chat-themes by Aohkne (MIT):
// https://github.com/Aohkne/ai-chat-themes (src/engine.js), cut to what
// office needs: no site adapters, the sidebar measured from our own element.
// Costs nothing when off: this module and each scene are their own chunks,
// loaded only by ThemeWallpaper. It stops on a hidden tab and, under
// reduced motion, draws one still frame.
import { GLYPHS, LOOSE, glyph, haloOf, setSide } from './kit'

const FPS_MS = 33

export function startWallpaper(canvas, scene, opts = {}) {
  const ctx = canvas.getContext('2d')
  if (!ctx) return { stop() {}, set() {} }
  const w = { ctx, scene, time: 0, last: 0, raf: 0, cell: 18, speed: 1, grid: false, ...opts }

  const sidebarCells = () => {
    const el = w.sidebar?.()
    setSide(Math.round((el?.getBoundingClientRect().right ?? 0) / w.cell))
  }

  const put = (px, py, c, sx, sy) => {
    ctx.globalAlpha = Math.max(0, Math.min(1, c.a))
    if (ctx.globalAlpha < 0.01) return
    const CELL = w.cell
    if (c.bg) {
      ctx.fillStyle = c.bg
      ctx.beginPath()
      ctx.roundRect(px + 2, py + 2, CELL - 4, CELL - 4, 3)
      ctx.fill()
    }
    ctx.fillStyle = c.fg
    ctx.fillText(c.ch ?? glyph(c.bg ? GLYPHS : LOOSE, sx, sy, w.time), px + CELL / 2, py + CELL / 2 + 1)
  }

  const draw = () => {
    const CELL = w.cell, t = w.time, W = innerWidth, H = innerHeight
    const cols = Math.ceil(W / CELL), rows = Math.ceil(H / CELL)
    const now = performance.now()
    if (!(now - w.measured < 1000)) { sidebarCells(); w.measured = now }
    ctx.clearRect(0, 0, W, H)
    ctx.globalAlpha = 1
    ctx.strokeStyle = 'rgba(160, 180, 255, .05)'
    ctx.beginPath()
    for (let x = 0; x <= cols; x++) { ctx.moveTo(x * CELL + 0.5, 0); ctx.lineTo(x * CELL + 0.5, H) }
    for (let y = 0; y <= rows; y++) { ctx.moveTo(0, y * CELL + 0.5); ctx.lineTo(W, y * CELL + 0.5) }
    ctx.stroke()
    if (scene.cell)
      for (let y = 0; y < rows; y++)
        for (let x = 0; x < cols; x++) {
          const c = scene.cell(x, y, cols, rows, t)
          if (c) put(x * CELL, y * CELL, c, x, y)
        }
    for (const s of scene.sprites?.(cols, rows, t) ?? []) {
      const ox = (w.grid ? Math.round(s.x) : s.x) * CELL, oy = (w.grid ? Math.round(s.y) : s.y) * CELL
      const m = s.a ?? 1, k = s.key * 997
      const bw = s.art ? s.art[0].length : s.w, bh = s.art ? s.art.length : s.h
      if (ox > W + CELL * 4 || oy > H + CELL * 4 || ox + (bw + 4) * CELL < 0 || oy + (bh + 4) * CELL < 0) continue
      if (s.glow) {
        const g = s.glow, cx = ox + bw / 2 * CELL, cy = oy + bh / 2 * CELL, r = g.r * CELL
        const grad = ctx.createRadialGradient(cx, cy, 0, cx, cy, r)
        grad.addColorStop(0, g.color)
        grad.addColorStop(1, `${g.color}00`)
        ctx.globalAlpha = Math.min(1, g.a)
        ctx.globalCompositeOperation = 'screen'
        ctx.fillStyle = grad
        ctx.fillRect(cx - r, cy - r, r * 2, r * 2)
        ctx.globalCompositeOperation = 'source-over'
      }
      const cells = []
      if (s.art) s.art.forEach((row, gy) => { for (let gx = 0; gx < row.length; gx++) if (s.pal[row[gx]]) cells.push([gx, gy, s.pal[row[gx]], gx + k, gy]) })
      else
        for (let gy = 0; gy < s.h; gy++)
          for (let gx = 0; gx < s.w; gx++) {
            const f = s.field(gx, gy)
            if (f) cells.push([gx, gy, f.c, (f.sx ?? gx) + k, f.sy ?? gy])
          }
      if (s.shadow)
        for (const [gx, gy, c] of cells)
          if (c.bg) put(ox + (gx + 0.8) * CELL, oy + (gy + 0.8) * CELL, { bg: s.shadow, fg: s.shadow, ch: '', a: 0.4 * m }, 0, 0)
      if (s.halo && s.art)
        for (const [gx, gy] of haloOf(s.art))
          put(ox + gx * CELL, oy + gy * CELL, { fg: s.halo, a: 0.3 * m }, gx + k, gy)
      for (const [gx, gy, c, sx, sy] of cells) put(ox + gx * CELL, oy + gy * CELL, m === 1 ? c : { ...c, a: c.a * m }, sx, sy)
    }
  }

  const size = () => {
    const dpr = devicePixelRatio || 1
    canvas.width = Math.round(innerWidth * dpr)
    canvas.height = Math.round(innerHeight * dpr)
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.font = `bold ${Math.round(w.cell * 13 / 22)}px ui-monospace, Menlo, Consolas, 'Cascadia Code', monospace`
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    w.measured = 0
    draw()
  }

  const calm = matchMedia('(prefers-reduced-motion: reduce)')
  const tick = (now) => {
    w.raf = requestAnimationFrame(tick)
    if (now - w.last < FPS_MS) return
    w.time += (w.last ? Math.min(now - w.last, 250) / 1000 : 0) * w.speed
    w.last = now
    draw()
  }
  // the loop runs only on a visible tab, with motion allowed
  const run = () => {
    cancelAnimationFrame(w.raf)
    w.raf = 0
    w.last = 0
    if (!document.hidden && !calm.matches && !w.still) w.raf = requestAnimationFrame(tick)
    else draw()
  }

  addEventListener('resize', size)
  document.addEventListener('visibilitychange', run)
  calm.addEventListener('change', run)
  size()
  run()

  return {
    stop() {
      cancelAnimationFrame(w.raf)
      removeEventListener('resize', size)
      document.removeEventListener('visibilitychange', run)
      calm.removeEventListener('change', run)
      setSide(0)
    },
    set(o) {
      const cell = o.cell !== undefined && o.cell !== w.cell
      Object.assign(w, o)
      if (cell) size()
      run()
    }
  }
}
