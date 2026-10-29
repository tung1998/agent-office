// Bản đồ văn phòng: the company's office in pixel art, drawn on a canvas (no
// images, no dependencies): the board room and the departments (rooms with
// wood floors, windows and a name plate), each agent a character at a desk.
// Working, it types and its screen lights its face; idle, it dozes off on the
// desk (zZ); paused, its chair is empty. Giving work, a sheet of paper flies
// to the other desk; a report a clipboard; a message a speech bubble.
//
//   const map = mount(el, { load: () => fetch('/data').then(r => r.json()) })
//   map.destroy()
//
// load returns the office map (office-db.mjs).

const LABELS = {
  director: 'Phòng Giám đốc',
  working: 'Đang làm',
  idle: 'Đang ngủ gật',
  off: 'Nghỉ',
  empty: 'Chưa có ai trong văn phòng',
  error: 'Không tải được bản đồ',
  legend: ['💤 rảnh', '⌨️ đang làm', '📄 giao việc', '📋 báo lại', '💬 trao đổi']
}

// an agent's shirt: office names a palette color (violet, sky…); none, one by its name
const PALETTE = { red: '#ef4444', orange: '#f97316', amber: '#f59e0b', yellow: '#eab308', lime: '#84cc16', green: '#22c55e', emerald: '#10b981', teal: '#14b8a6', cyan: '#06b6d4', sky: '#0ea5e9', blue: '#3b82f6', indigo: '#6366f1', violet: '#8b5cf6', purple: '#a855f7', fuchsia: '#d946ef', pink: '#ec4899', rose: '#f43f5e', slate: '#64748b', gray: '#6b7280', neutral: '#737373', stone: '#78716c', zinc: '#71717a' }
const SHIRTS = Object.values(PALETTE).slice(0, 17)
const HAIR = ['#2b1a10', '#4a2c17', '#7a4a22', '#b07a3a', '#1f2937', '#6b2a12', '#9ca3af', '#d6a85c']
const SKIN = ['#f6d3b3', '#eab88f', '#d39a6a', '#a8714a', '#7a4f32']

const hash = s => { let h = 7; for (const c of String(s)) h = (h * 31 + c.codePointAt(0)) >>> 0; return h }
const shirtOf = d => PALETTE[d.color] || (/^#[0-9a-f]{3,8}$/i.test(d.color || '') ? d.color : SHIRTS[hash(d.name) % SHIRTS.length])
const shade = (hex, k) => {
  const n = parseInt(hex.slice(1).padEnd(6, '0').slice(0, 6), 16)
  const f = c => Math.max(0, Math.min(255, Math.round(c * k)))
  return `rgb(${f(n >> 16)},${f((n >> 8) & 255)},${f(n & 255)})`
}

// sizes in art pixels (drawn at SCALE screen pixels each)
const SLOT_W = 44, SLOT_H = 50, WALL = 30, PAD = 10, GAP = 10

const ICON = { delegate: '📄', report: '📋', message: '💬' }

export function mount(el, opts = {}) {
  const L = { ...LABELS, ...(opts.labels || {}) }
  const every = opts.every || 2000
  const doc = el.ownerDocument
  const win = doc.defaultView
  const wrap = doc.createElement('div')
  wrap.style.cssText = 'position:relative;height:100%;overflow:auto;background:#1b1f2a'
  const canvas = doc.createElement('canvas')
  canvas.style.cssText = 'display:block;image-rendering:pixelated'
  const tip = doc.createElement('div')
  tip.style.cssText = 'position:absolute;pointer-events:none;display:none;max-width:240px;padding:6px 9px;border-radius:6px;background:#0f172aee;color:#f8fafc;font:12px/1.35 system-ui,sans-serif;box-shadow:0 4px 14px #0006;z-index:2;white-space:pre-line'
  const note = doc.createElement('div')
  note.style.cssText = 'display:none;padding:28px;text-align:center;color:#cbd5e1;font:14px system-ui,sans-serif'
  const legend = doc.createElement('div')
  legend.style.cssText = 'display:flex;gap:14px;flex-wrap:wrap;padding:6px 12px 10px;color:#94a3b8;font:12px system-ui,sans-serif'
  legend.innerHTML = L.legend.map(x => `<span>${x.replace(/[<>&]/g, '')}</span>`).join('')
  wrap.append(note, canvas, legend, tip)
  el.appendChild(wrap)
  const ctx = canvas.getContext('2d')

  let map = { rooms: [], events: [] }
  let layout = { rooms: [], desks: new Map(), w: 0, h: 0 }
  let scale = 3
  let seen = null
  let fx = [] // flights and bubbles playing
  let hover = null
  let gone = false
  let timer = 0
  let raf = 0
  let loaded = false

  // ---- layout: rooms flow left to right, the board room first ----
  function place() {
    const cw = wrap.clientWidth || 800
    scale = cw >= 1200 ? 3 : 2
    const maxW = Math.floor(cw / scale) - GAP * 2
    const rooms = []
    let x = GAP, y = GAP, rowH = 0
    map.rooms.forEach((r, i) => {
      const n = Math.max(r.desks.length, 1)
      let cols = Math.min(n, i === 0 ? 6 : 4, Math.max(1, Math.floor((maxW - PAD * 2) / SLOT_W)))
      if (i === 0 && n <= 6) cols = n
      const rows = Math.ceil(n / cols)
      const w = Math.max(cols * SLOT_W + PAD * 2, 120)
      const h = WALL + rows * SLOT_H + PAD + 4
      if (x > GAP && x + w > maxW + GAP) { x = GAP; y += rowH + GAP; rowH = 0 }
      rooms.push({ ...r, hq: i === 0, x, y, w, h, cols })
      x += w + GAP
      rowH = Math.max(rowH, h)
    })
    const desks = new Map()
    for (const room of rooms) {
      room.desks.forEach((d, i) => {
        const c = i % room.cols, rr = Math.floor(i / room.cols)
        const used = Math.min(room.cols, room.desks.length - rr * room.cols)
        const off = (room.w - PAD * 2 - used * SLOT_W) / 2
        const dx = room.x + PAD + off + c * SLOT_W
        const dy = room.y + WALL + 4 + rr * SLOT_H
        desks.set(`${room.project_id}|${d.id}`, { d, room, x: dx, y: dy, seed: hash(d.id) })
      })
    }
    const w = Math.max(...rooms.map(r => r.x + r.w), 100) + GAP
    const h = Math.max(...rooms.map(r => r.y + r.h), 60) + GAP
    layout = { rooms, desks, w, h }
    const dpr = win.devicePixelRatio || 1
    canvas.width = w * scale * dpr
    canvas.height = h * scale * dpr
    canvas.style.width = `${w * scale}px`
    canvas.style.height = `${h * scale}px`
  }

  // ---- drawing helpers (art pixels) ----
  const R = (x, y, w, h, c) => { ctx.fillStyle = c; ctx.fillRect(Math.round(x), Math.round(y), w, h) }

  function drawRoom(r, t) {
    const { x, y, w, h } = r
    // floor
    if (r.hq) {
      for (let i = 0; i < h - WALL; i += 4) R(x, y + WALL + i, w, 4, i % 8 ? '#6b4a2e' : '#71502f')
      R(x + 8, y + WALL + 5, w - 16, h - WALL - 10, '#7f1d2d')
      R(x + 10, y + WALL + 7, w - 20, h - WALL - 14, '#9b2335')
      ctx.strokeStyle = '#d4a73a'; ctx.lineWidth = 1
      ctx.strokeRect(x + 11.5, y + WALL + 8.5, w - 23, h - WALL - 17)
    } else {
      for (let i = 0; i < h - WALL; i += 4) {
        R(x, y + WALL + i, w, 4, (i / 4) % 2 ? '#b98f58' : '#c49a62')
        for (let j = ((i / 4) % 3) * 9; j < w; j += 27) R(x + j, y + WALL + i, 1, 4, '#a47b48')
      }
    }
    // wall, trim, baseboard
    const wall = r.hq ? '#e9dcc3' : '#dfe6ee'
    R(x, y, w, WALL, wall)
    R(x, y, w, 3, r.hq ? '#8a6a3e' : '#7b8796')
    R(x, y + WALL - 3, w, 3, r.hq ? '#6b4f2c' : '#5b6675')
    // windows (sky shifts with the hour)
    const hour = new Date().getHours()
    const sky = hour >= 6 && hour < 18 ? ['#9fd4ff', '#c6e6ff'] : hour >= 18 && hour < 20 ? ['#f59e7b', '#fbc79a'] : ['#1e2a4a', '#2c3b66']
    ctx.font = 'bold 6px system-ui, sans-serif'
    const name = r.hq && layout.rooms.length > 1 ? `👑 ${r.name} · ${L.director}` : r.name
    const tw = Math.min(ctx.measureText(name).width + 10, w - 16)
    const px = x + (w - tw) / 2
    for (let wx = x + 22; wx + 22 < x + w - 30; wx += 40) {
      if (wx + 24 > px && wx < px + tw + 2) continue
      R(wx, y + 6, 22, 15, '#6b5a44')
      R(wx + 1, y + 7, 20, 13, sky[0])
      R(wx + 1, y + 7, 20, 5, sky[1])
      R(wx + 10, y + 7, 1, 13, '#6b5a44')
      R(wx + 1, y + 13, 20, 1, '#6b5a44')
      if (hour < 6 || hour >= 20) { R(wx + 4, y + 9, 1, 1, '#fff'); R(wx + 15, y + 11, 1, 1, '#fde68a') }
    }
    // a clock (the real time) and the room's furniture
    clock(x + w - 20, y + 6)
    if (r.hq) shelf(x + 4, y + 4)
    else cooler(x + w - 11, y + WALL - 14)
    // name plate
    R(px, y + 4, tw, 10, r.hq ? '#3b2a17' : '#263241')
    R(px, y + 4, tw, 1, r.hq ? '#d4a73a' : '#5b7089')
    ctx.fillStyle = r.hq ? '#fde68a' : '#e2e8f0'
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
    ctx.fillText(name, x + w / 2, y + 9.5, tw - 6)
    // plants by the wall
    if (!r.hq) plant(x + 3, y + WALL - 2, t)
    else plant(x + w - 11, y + WALL - 2, t + 900)
    // the room's outline
    ctx.strokeStyle = '#0b0f17'; ctx.lineWidth = 2
    ctx.strokeRect(x - 1, y - 1, w + 2, h + 2)
  }

  function clock(x, y) {
    const now = new Date()
    R(x + 1, y, 10, 12, '#4b3b2a'); R(x, y + 1, 12, 10, '#4b3b2a'); R(x + 1, y + 1, 10, 10, '#f8fafc')
    ctx.strokeStyle = '#111827'; ctx.lineWidth = 0.8; ctx.lineCap = 'round'
    const hand = (a, len) => { ctx.beginPath(); ctx.moveTo(x + 6, y + 6); ctx.lineTo(x + 6 + Math.sin(a) * len, y + 6 - Math.cos(a) * len); ctx.stroke() }
    hand((now.getHours() % 12 + now.getMinutes() / 60) / 12 * Math.PI * 2, 2.6)
    hand(now.getMinutes() / 60 * Math.PI * 2, 3.8)
  }

  function shelf(x, y) {
    R(x, y, 16, 22, '#5b3d22'); R(x + 1, y + 1, 14, 20, '#3a2614')
    const books = ['#ef4444', '#3b82f6', '#eab308', '#22c55e', '#a855f7', '#f97316']
    for (let row = 0; row < 3; row++) {
      R(x + 1, y + 7 + row * 7, 14, 1, '#5b3d22')
      for (let b = 0; b < 4; b++) R(x + 2 + b * 3, y + 2 + row * 7, 2, 5, books[(row * 2 + b) % books.length])
    }
  }

  function cooler(x, y) {
    R(x + 1, y, 6, 6, '#93c5fd'); R(x + 2, y + 1, 2, 3, '#dbeafe')
    R(x, y + 6, 8, 10, '#e5e7eb'); R(x, y + 6, 8, 1, '#cbd5e1'); R(x + 2, y + 8, 2, 1, '#ef4444'); R(x + 5, y + 8, 1, 1, '#3b82f6')
  }

  function plant(x, y, t) {
    const sway = Math.sin(t / 900) > 0.6 ? 1 : 0
    R(x + 2, y + 6, 6, 5, '#b45309'); R(x + 2, y + 6, 6, 1, '#92400e')
    R(x + 1 + sway, y, 3, 6, '#15803d'); R(x + 4, y - 3, 3, 9, '#16a34a'); R(x + 6 - sway, y + 1, 3, 5, '#22a35a')
  }

  function drawDesk(s, t) {
    const { d, x, y, seed } = s
    const shirt = shirtOf(d)
    const hair = HAIR[seed % HAIR.length], skin = SKIN[(seed >>> 3) % SKIN.length]
    const state = d.off ? 'off' : d.busy ? 'busy' : 'idle'
    const cx = x + 12 // the character's left
    // chair back
    R(cx - 1, y + 6, 14, 14, '#2f3a4a'); R(cx, y + 7, 12, 12, '#3d4a5c')
    if (state !== 'off') {
      const sleep = state === 'idle'
      const hy = sleep ? y + 9 : y + 2 + (state === 'busy' && Math.floor(t / 600 + seed) % 6 === 0 ? 1 : 0)
      // body
      R(cx + 1, y + 14, 10, 8, shirt); R(cx + 1, y + 14, 10, 1, shade(shirt, 1.15)); R(cx + 5, y + 14, 2, 3, shade(shirt, 0.7))
      // head
      if (sleep) {
        // head down on folded arms, turned aside
        R(cx, y + 17, 12, 3, shirt); R(cx + 1, y + 17, 3, 2, skin); R(cx + 8, y + 17, 3, 2, skin)
        R(cx + 2, hy + 1, 8, 6, skin); R(cx + 1, hy, 10, 4, hair); R(cx + 2, hy - 1, 8, 2, hair)
        R(cx + 3, hy + 5, 2, 1, shade(skin, 0.75)); R(cx + 7, hy + 5, 2, 1, shade(skin, 0.75)) // closed eyes
      } else {
        R(cx + 2, hy + 3, 8, 8, skin)
        R(cx + 1, hy + 1, 10, 4, hair); R(cx + 2, hy, 8, 2, hair); R(cx + 1, hy + 4, 1, 3, hair); R(cx + 10, hy + 4, 1, 3, hair)
        const blink = Math.floor(t / 140 + seed) % 28 === 0
        R(cx + 4, hy + 6, 1, blink ? 1 : 2, '#1f2937'); R(cx + 7, hy + 6, 1, blink ? 1 : 2, '#1f2937')
        R(cx + 5, hy + 9, 2, 1, shade(skin, 0.75))
        if (state === 'busy') {
          // the screen's light on the face
          ctx.fillStyle = 'rgba(125,211,252,0.28)'; ctx.fillRect(cx + 2, hy + 3, 8, 8)
        }
      }
      if (s.room.head === d.id) { // the head of the room
        const ky = hy - 4
        R(cx + 3, ky + 1, 6, 2, '#facc15'); R(cx + 3, ky, 1, 1, '#facc15'); R(cx + 5, ky - 1, 2, 2, '#facc15'); R(cx + 8, ky, 1, 1, '#facc15')
      }
    }
    // desk, with its shadow
    ctx.fillStyle = 'rgba(0,0,0,0.18)'; ctx.fillRect(x + 5, y + 32, 36, 2)
    R(x + 3, y + 20, 38, 4, '#b07a43'); R(x + 3, y + 20, 38, 1, '#c98f52')
    R(x + 4, y + 24, 36, 8, '#7a5230'); R(x + 5, y + 25, 15, 6, '#8a5f38'); R(x + 24, y + 25, 15, 6, '#8a5f38')
    R(x + 12, y + 27, 3, 1, '#d6b27a'); R(x + 31, y + 27, 3, 1, '#d6b27a')
    // monitor (its back to us) and keyboard
    const mx = x + 28
    R(mx, y + 9, 12, 9, '#2b3240'); R(mx + 1, y + 10, 10, 7, '#3a4354'); R(mx + 5, y + 18, 2, 2, '#2b3240'); R(mx + 3, y + 19, 6, 1, '#2b3240')
    if (state === 'busy') {
      ctx.fillStyle = 'rgba(125,211,252,0.35)'; ctx.fillRect(mx - 3, y + 8, 2, 11)
      R(mx + 9, y + 11, 1, 1, Math.floor(t / 300) % 2 ? '#22c55e' : '#14532d')
    }
    R(x + 13, y + 19, 12, 2, '#e5e7eb'); R(x + 14, y + 19, 10, 1, '#cbd5e1')
    if (state === 'busy') { // hands on the keys
      const f = Math.floor(t / 110 + seed) % 2
      R(x + 13 + f, y + 18 - f, 3, 2, SKIN[(seed >>> 3) % SKIN.length]); R(x + 21 - f, y + 18 - (1 - f), 3, 2, SKIN[(seed >>> 3) % SKIN.length])
    }
    // a mug, a lamp off for an empty chair
    R(x + 5, y + 16, 4, 4, '#f8fafc'); R(x + 9, y + 17, 1, 2, '#f8fafc'); R(x + 5, y + 16, 4, 1, '#6b4423')
    if (state === 'off') { ctx.fillStyle = 'rgba(15,23,42,0.35)'; ctx.fillRect(x + 2, y + 4, 40, 30) }
    // zZ
    if (state === 'idle') {
      ctx.font = 'bold 6px monospace'; ctx.textAlign = 'left'; ctx.textBaseline = 'alphabetic'
      for (let k = 0; k < 3; k++) {
        const p = ((t / 2400 + k / 3 + (seed % 7) / 7) % 1)
        ctx.fillStyle = `rgba(147,197,253,${(1 - p).toFixed(2)})`
        ctx.font = `bold ${4 + k}px monospace`
        ctx.fillText('z', cx + 11 + p * 7, y + 8 - p * 12)
      }
    }
    // name and state
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
    ctx.font = 'bold 5.5px system-ui, sans-serif'
    const label = d.name
    const lw = Math.min(ctx.measureText(label).width + 8, SLOT_W - 2)
    R(x + 22 - lw / 2, y + 35, lw, 8, 'rgba(15,23,42,0.72)')
    R(x + 22 - lw / 2 + 2, y + 38, 2, 2, state === 'busy' ? '#22c55e' : state === 'idle' ? '#60a5fa' : '#64748b')
    ctx.fillStyle = '#f1f5f9'
    ctx.fillText(label, x + 23, y + 39.5, lw - 8)
  }

  // ---- effects ----
  function center(s) { return { x: s.x + 18, y: s.y + 4 } }
  function deskOf(project, id) {
    const room = layout.rooms.find(r => r.project_id === project)
    const agent = id || room?.head
    return agent ? layout.desks.get(`${project}|${agent}`) : null
  }
  function play(ev, delay) {
    const from = deskOf(ev.from_project, ev.from)
    const to = deskOf(ev.to_project, ev.to)
    const t0 = performance.now() + delay
    if (from && ev.text) fx.push({ kind: 'say', at: from, text: `${ICON[ev.kind] || '💬'} ${ev.text}`, t0, dur: 4200 })
    if (from && to && from !== to) {
      fx.push({ kind: 'fly', icon: ICON[ev.kind] || '💬', from, to, t0: t0 + 300, dur: 1400 })
      fx.push({ kind: 'pop', at: to, icon: ev.kind === 'delegate' ? '❗' : ev.kind === 'report' ? '✅' : '💬', t0: t0 + 1700, dur: 1500 })
    }
  }
  function drawFx(now) {
    fx = fx.filter(f => now < f.t0 + f.dur)
    for (const f of fx) {
      const p = (now - f.t0) / f.dur
      if (p < 0) continue
      if (f.kind === 'fly') {
        const a = center(f.from), b = center(f.to)
        const e = p < 0.5 ? 2 * p * p : 1 - (-2 * p + 2) ** 2 / 2
        const x = a.x + (b.x - a.x) * e
        const y = Math.max(6, a.y + (b.y - a.y) * e - Math.sin(Math.PI * e) * Math.min(40, 16 + Math.hypot(b.x - a.x, b.y - a.y) * 0.12))
        ctx.fillStyle = 'rgba(0,0,0,0.25)'
        ctx.beginPath(); ctx.ellipse(a.x + (b.x - a.x) * e, a.y + (b.y - a.y) * e + 14, 4, 1.5, 0, 0, 7); ctx.fill()
        ctx.font = '10px system-ui'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
        ctx.save(); ctx.translate(x, y); ctx.rotate(f.icon === '📄' ? e * Math.PI * 2 : 0); ctx.fillText(f.icon, 0, 0); ctx.restore()
      } else if (f.kind === 'pop') {
        const c = center(f.at)
        const k = p < 0.2 ? p / 0.2 : 1
        bubble(c.x + 6, c.y - 6 - k * 3, f.icon, 1 - Math.max(0, (p - 0.8) / 0.2))
      } else if (f.kind === 'say') {
        const c = center(f.at)
        bubble(c.x, c.y - 8, f.text, p < 0.08 ? p / 0.08 : p > 0.85 ? (1 - p) / 0.15 : 1)
      }
    }
  }
  function bubble(x, y, text, alpha) {
    ctx.save()
    ctx.globalAlpha = Math.max(0, Math.min(1, alpha))
    ctx.font = '6px system-ui, sans-serif'
    const t = text.length > 34 ? text.slice(0, 33) + '…' : text
    const w = Math.min(ctx.measureText(t).width + 8, 150), h = 10
    const bx = Math.max(2, Math.min(layout.w - w - 2, x - w / 2)), by = y - h
    R(bx + 1, by, w - 2, h, '#ffffff'); R(bx, by + 1, w, h - 2, '#ffffff')
    R(bx + 1, by + h, w - 2, 1, '#94a3b8')
    R(x - 1, by + h, 3, 2, '#ffffff'); R(x, by + h + 2, 1, 1, '#ffffff')
    ctx.fillStyle = '#0f172a'; ctx.textAlign = 'left'; ctx.textBaseline = 'middle'
    ctx.fillText(t, bx + 4, by + h / 2 + 0.5, w - 8)
    ctx.restore()
  }

  function frame(now) {
    if (gone) return
    raf = win.requestAnimationFrame(frame)
    if (!layout.rooms.length) return
    const dpr = win.devicePixelRatio || 1
    ctx.setTransform(scale * dpr, 0, 0, scale * dpr, 0, 0)
    ctx.imageSmoothingEnabled = false
    ctx.clearRect(0, 0, layout.w, layout.h)
    for (const r of layout.rooms) drawRoom(r, now)
    for (const s of layout.desks.values()) {
      try { drawDesk(s, now) } catch (e) { console.warn('office-map: desk', s.d?.name, e) } // one desk never stops the others
    }
    if (hover) {
      ctx.strokeStyle = '#fde68a'; ctx.lineWidth = 1
      ctx.strokeRect(hover.x + 1.5, hover.y + 0.5, SLOT_W - 3, SLOT_H - 6)
    }
    drawFx(now)
  }

  // ---- hover and click ----
  function at(e) {
    const b = canvas.getBoundingClientRect()
    const x = (e.clientX - b.left) / scale, y = (e.clientY - b.top) / scale
    for (const s of layout.desks.values()) if (x >= s.x && x < s.x + SLOT_W && y >= s.y && y < s.y + SLOT_H - 6) return s
    return null
  }
  canvas.addEventListener('mousemove', e => {
    hover = at(e)
    canvas.style.cursor = hover && opts.onOpen ? 'pointer' : 'default'
    if (!hover) { tip.style.display = 'none'; return }
    const d = hover.d
    const what = d.off ? L.off : d.busy ? `${L.working}: ${d.doing || '…'}` : L.idle
    tip.textContent = `${d.name}${d.role ? `\n${d.role}` : ''}\n${what}`
    const b = wrap.getBoundingClientRect()
    tip.style.display = 'block'
    tip.style.left = `${Math.min(e.clientX - b.left + wrap.scrollLeft + 14, wrap.scrollWidth - 250)}px`
    tip.style.top = `${e.clientY - b.top + wrap.scrollTop + 14}px`
  })
  canvas.addEventListener('mouseleave', () => { hover = null; tip.style.display = 'none' })
  canvas.addEventListener('click', e => {
    const s = at(e)
    if (s && opts.onOpen) opts.onOpen({ projectId: s.room.project_id, agentId: s.d.id })
  })
  const ro = new win.ResizeObserver(() => { if (map.rooms.length) place() })
  ro.observe(wrap)

  async function tick() {
    if (gone) return
    try {
      const next = await opts.load()
      if (gone) return
      loaded = true
      const shape = m => JSON.stringify(m.rooms.map(r => [r.project_id, r.name, r.desks.map(d => d.id)]))
      const moved = shape(next) !== shape(map)
      map = next
      if (!map.rooms.some(r => r.desks.length)) {
        note.textContent = L.empty; note.style.display = 'block'; canvas.style.display = 'none'
      } else {
        note.style.display = 'none'; canvas.style.display = 'block'
        if (moved) place()
        else for (const r of map.rooms) for (const d of r.desks) { const s = layout.desks.get(`${r.project_id}|${d.id}`); if (s) s.d = d }
        for (const lr of layout.rooms) { const r = map.rooms.find(x => x.project_id === lr.project_id); if (r) lr.head = r.head }
      }
      const evs = map.events || []
      const key = e => `${e.at}|${e.kind}|${e.from}|${e.to}|${e.to_project}`
      if (seen) evs.filter(e => !seen.has(key(e))).forEach((e, i) => play(e, i * 500))
      seen = new Set(evs.map(key))
    } catch (e) {
      if (!loaded) { note.textContent = `${L.error}${e?.message ? `: ${e.message}` : ''}`; note.style.display = 'block'; canvas.style.display = 'none' }
    }
    if (!gone) timer = setTimeout(tick, every)
  }
  tick()
  raf = win.requestAnimationFrame(frame)

  return {
    refresh: () => { clearTimeout(timer); tick() },
    destroy() {
      gone = true
      clearTimeout(timer)
      win.cancelAnimationFrame(raf)
      ro.disconnect()
      wrap.remove()
    }
  }
}
