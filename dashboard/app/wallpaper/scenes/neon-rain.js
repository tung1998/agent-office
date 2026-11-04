// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { SIDE, hash, drift, near, sketch, frame, shade } from '../kit'

const STREET = 3;
let city;
const cityOf = (cols, rows) => {
  if (city?.cols !== cols || city.rows !== rows || city.side !== SIDE) {
    const mid = SIDE + (cols - SIDE) / 2, half = Math.max(1, (cols - SIDE) / 2);
    const row = (seed, lo, hi, pow) => {
      const out = [];
      for (let x = 0, b = 0; x < cols; b++) {
        const w = 4 + Math.floor(hash(b, seed) * 5), e = Math.min(1, Math.abs(x + w / 2 - mid) / half);
        const h = Math.round(rows * (lo + hi * e ** pow * (.6 + .4 * hash(b, seed + 1))));
        for (let i = 0; i < w; i++) out[x + i] = [h, x, x + w - 1, b];
        x += w;
      }
      return out;
    };
    city = { cols, rows, side: SIDE, near: row(1, .08, .52, 2), far: row(5, .16, .48, 1.4) };
  }
  return city;
};
const CITY = {
  wall: { bg: '#1b2242', fg: '#2b355a', a: .95 }, roof: { bg: '#2a3358', fg: '#3a4570', a: .95 },
  dark: { bg: '#232b4e', fg: '#323c64', a: .95 }, far: { bg: '#121730', fg: '#1b2140', a: .75 },
  farWin: { bg: '#6a5a3a', fg: '#2a2416', a: .5 },
  win: [{ bg: '#f3c56b', fg: '#6a4a10', a: .85 }, { bg: '#ffe2a0', fg: '#7a5a18', a: .85 }, { bg: '#8fd3ff', fg: '#1d4a6a', a: .75 }],
};
const RAIN_V = 11, RAIN_K = .3;
const rain = (x, y, t) => {
  const q = Math.floor(y - t * RAIN_V), track = Math.floor(x + y * RAIN_K), m = ((q % 3) + 3) % 3;
  if (m < 2 && hash(track, Math.floor(q / 3), 27) < .09) return { ch: '/', fg: '#a9c4f0', a: m ? .6 : .28 };
};
const SMEAR = ['#ff5fa2', '#4fe3ff', '#f3c56b'];
const street = (x, y, t) => {
  if (y === 0) return { ch: '_', fg: '#3a4570', a: .55 };
  if (hash(x, y, 40 + Math.floor(t * 3)) < .025) return { ch: 'o', fg: '#a9c4f0', a: .5 };
  if (hash(x, Math.floor(t * 1.5) + y, 41) < .12) return { ch: y === 1 ? '|' : '~', fg: SMEAR[Math.floor(hash(x, 42) * 3)], a: .28 };
};
const neon = (word, c) => {
  const tube = ch => ({ ch, fg: c, a: .9 }), pal = { '+': tube('+'), '-': tube('-'), '|': tube('|') };
  for (const ch of word) {
    pal[ch] = { bg: '#1c0b1a', fg: c, a: .95, ch };
    pal[ch.toLowerCase()] = { bg: '#1c0b1a', fg: shade(c, -.55), a: .9, ch };
  }
  const art = sketch(3, word.length + 2, [[0, 0, '+-+'], ...[...word].map((ch, i) => [i + 1, 0, `|${ch}|`]), [word.length + 1, 0, '+-+']]);
  return { c, pal, arts: [art, ...[...word].map((_, i) => art.map((r, y) => (y === i + 1 ? r.toLowerCase() : r)))] };
};
const SIGNS = [neon('RAMEN', '#ff5fa2'), neon('HOTEL', '#4fe3ff')];
const signs = (cols, rows, t) => {
  const { near } = cityOf(cols, rows), ground = rows - STREET, W = cols - SIDE;
  return SIGNS.flatMap((s, i) => {
    let best;
    for (let x = Math.floor(SIDE + (i ? W * .7 : 0)); x < (i ? cols : SIDE + W * .3); x++) if (!best || near[x][0] > best[0]) best = near[x];
    if (!best || best[0] < s.arts[0].length + 3) return [];
    const k = Math.floor(t * 4), art = hash(k, i, 51) < .1 ? s.arts[1 + Math.floor(hash(k, i, 52) * (s.arts.length - 1))] : s.arts[0];
    const a = (t + i * 4.5) % 9 < .3 ? .3 : 1;
    return [{ art, pal: s.pal, key: 70 + i, a, x: i ? best[1] - 2 : best[2], y: ground - best[0] + 2, glow: { r: 6, color: s.c, a: .35 * a } }];
  });
};
const WALK = [
  ['..uUu..', '.uUUUu.', 'uUUUUUu', 'd..|..d', '...h...', '..cCc..', '..cCc..', '..l.l..'],
  ['..uUu..', '.uUUUu.', 'uUUUUUu', '...|...', 'd..h..d', '..cCc..', '..cCc..', '...l...'],
];
const walkPal = c => ({
  U: { bg: c, fg: shade(c, -.6), a: .95 }, u: { bg: shade(c, -.25), fg: shade(c, -.7), a: .95 },
  d: { ch: '.', fg: '#bcd2f5', a: .6 }, '|': { ch: '|', fg: '#8a92b0', a: .9 },
  h: { bg: '#3a3550', fg: '#1a1828', a: .95 }, c: { bg: '#252a42', fg: '#121526', a: .95 },
  C: { bg: '#2f3554', fg: '#151a2e', a: .95 }, l: { bg: '#1c2034', fg: '#0c0e18', a: .95 },
});
const WALKERS = [[.97, 1.5, false, walkPal('#ff5fa2')], [.98, 1.1, true, walkPal('#f2c14e')]];
const STORM = 23;
const boltArt = k => {
  const out = [];
  for (let y = 0, c = 4; y < 12; y++) {
    const s = Math.floor(hash(k, y, 61) * 3) - 1;
    c = Math.max(0, Math.min(8, c + s));
    out.push('.'.repeat(c) + (s < 0 ? '/' : s > 0 ? '\\' : '|') + '.'.repeat(8 - c));
  }
  return out;
};
const BOLT_PAL = Object.fromEntries(['/', '\\', '|'].map(ch => [ch, { ch, fg: '#eef3ff', a: 1 }]));
const lightning = (cols, rows, t) => {
  const err = root.dataset.themeEvent === 'error', k = err ? -1 : Math.floor(t / STORM), p = err ? 0 : t - k * STORM;
  if (!(p < .1 || (p > .2 && p < .32))) return [];
  const W = cols - SIDE, fx = hash(k, 62) < .5 ? .06 + .14 * hash(k, 63) : .78 + .14 * hash(k, 63);
  return [{ art: boltArt(k), pal: BOLT_PAL, key: 80, x: SIDE + W * fx, y: rows * .03, glow: { r: 16, color: '#a9bcff', a: .45 } }];
};

export default {
  cell: (x, y, cols, rows, t) => {
    const { near, far } = cityOf(cols, rows), ground = rows - STREET;
    if (y >= ground) return street(x, y - ground, t);
    const [h, x0, x1, b] = near[x] ?? [0], top = ground - h;
    if (y >= top) {
      if (y === top) return CITY.roof;
      if (x > x0 && x < x1 && (x - x0) % 2 === 1 && (ground - y) % 2 === 0 && y > top + 1) {
        const lit = (hash(x, y, 21) < .45) !== (hash(x, y, 22 + Math.floor(t / 7 + hash(x, y, 24) * 7)) < .08);
        const s = hash(x, y, 23);
        return lit ? CITY.win[s < .6 ? 0 : s < .85 ? 1 : 2] : CITY.dark;
      }
      return CITY.wall;
    }
    if (y === top - 1 && x === Math.floor((x0 + x1) / 2) && hash(b, 31) < .35)
      return { ch: '*', fg: '#ff4d5e', a: Math.sin(t * 2.5 + b) > .3 ? .95 : .2 };
    const [fh, fx0, fx1] = far[x] ?? [0];
    if (y >= ground - fh)
      return fx1 - fx0 > 2 && (x - fx0) % 2 === 1 && (ground - y) % 3 === 0 && hash(x, y, 25) < .3 ? CITY.farWin : CITY.far;
    return rain(x, y, t);
  },
  sprites: (cols, rows, t) => [
    ...lightning(cols, rows, t),
    ...signs(cols, rows, t),
    ...WALKERS.map(([f, v, left, pal], i) => {
      const run = drift(cols, f, t, v, 7);
      return { art: frame(WALK, t, 3, i), pal, key: 90 + i, x: (left ? cols - run : run) - 3.5, y: rows - STREET - 8 };
    }),
  ],
};
