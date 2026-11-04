// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, twinkle, frame } from '../kit'

const CABIN_LIT = ['............CC....', '.......NNNNNCC....', '.....NNNNNNNNNN...', '...NNNNNNNNNNNNNN.',
  '.NNNRRRRRRRRRRRNNN', '..LLLLLLLLLLLLLLL.', '..lYyYlLLLLLLDDDl.', '..LyyyLLLLLLLDDDL.', '..lYyYlLLLLLLDdDl.',
  '..lllllllllllDDDl.', 'NNNNNNNNNNNNNNNNNN'];
const CABIN_ARTS = [CABIN_LIT, CABIN_LIT.map((r, y) => (y > 5 && y < 9 ? r.replace(/Y/g, 'Z') : r))];
const CABIN_PAL = {
  N: { bg: '#e8f0fb', fg: '#9fb4d4', a: .95 }, C: { bg: '#3a2a24', fg: '#1a120e', a: .95 }, R: { bg: '#2a1c18', fg: '#120b08', a: .95 },
  L: { bg: '#5a3b26', fg: '#2e1e12', a: .95 }, l: { bg: '#4a2f1e', fg: '#24160c', a: .95 }, D: { bg: '#3a2516', fg: '#1a1008', a: .95 },
  d: { bg: '#e0a458', fg: '#5a3a10', a: .95 }, y: { bg: '#5a3b26', fg: '#2e1e12', a: .95 },
  Y: { bg: '#ffd38a', fg: '#c47a2a', a: 1 }, Z: { bg: '#f6b860', fg: '#a8601c', a: 1 },
};
const PINE = ['....N....', '...NGN...', '..GGGGG..', '...GNG...', '..NGGGN..', '.GGGGGGG.', '..GGNGG..', '.NGGGGGGN',
  'GGGGGGGGG', '....T....', '....T....'];
const sway = (art, k) => art.map((r, y) => (y < 4 ? (k > 0 ? '.' + r.slice(0, -1) : r.slice(1) + '.') : r));
const PINES = [PINE, sway(PINE, 1), PINE, sway(PINE, -1)];
const PINE_PAL = {
  G: { bg: '#1f3a35', fg: '#0d1e1b', a: .95 }, N: { bg: '#dbe6f6', fg: '#8ea2c4', a: .92 }, T: { bg: '#3a2516', fg: '#1a1008', a: .95 },
};
const TREES = [[.34, 1], [.78, .7], [.86, 1], [.94, .8]];
const GROUND = .84;
const groundAt = (x, rows) => Math.round(rows * GROUND + Math.sin(x * .09) * 1.2);
const cabinAt = (cols, rows) => ({ x: Math.round(cols * .05), y: groundAt(Math.round(cols * .05) + 9, rows) - 10 });
const peak = (x, cols, rows) => Math.max(0,
  rows * .34 - Math.abs(x - cols * .1) * .9, rows * .26 - Math.abs(x - cols * .24) * 1.1,
  rows * .38 - Math.abs(x - cols * .9) * .85, rows * .22 - Math.abs(x - cols * .74) * 1.2);

export default {
  cell: (x, y, cols, rows, t) => {
    const ground = groundAt(x, rows), ridge = Math.round(rows * GROUND - peak(x, cols, rows));
    const fall = (v, odds, seed) => hash(x + Math.round(Math.sin(y * .3 + t * .5 + seed) * 1.5), Math.floor(y - t * v), seed) < odds;
    if (y >= ground) {
      if (hash(x, y, 21) < .02) return { ch: '*', fg: '#ffffff', a: .3 + .5 * Math.sin(t * 2 + x * 7) ** 2 };
      return { bg: '#3d4d6e', fg: '#56688c', a: .85 };
    }
    if (y >= ridge) {
      if (y < ridge + 2 + hash(x, 13) * 2 && peak(x, cols, rows) > 6) return { bg: '#b8c7df', fg: '#8195b8', a: .7 };
      return { bg: '#18233d', fg: '#222f4d', a: .9 };
    }
    const { x: cx, y: cy } = cabinAt(cols, rows), dy = cy + 1 - y;
    if (dy > 0 && dy < 12) {
      const mid = cx + 12.5 + Math.sin(t * .8 - dy * .5) * 1.2 + dy * .45;
      if (Math.abs(x - mid) <= 1 + dy * .12 && hash(x, Math.floor(y + t * 1.6), 17) < .55)
        return { ch: dy < 4 ? 'o' : '~', fg: '#c9d3e6', a: .4 * (1 - dy / 12) };
    }
    if (fall(3.2, .012, 31)) return { ch: '*', fg: '#ffffff', a: .7 };
    if (fall(1.6, .02, 33)) return { ch: '.', fg: '#dfe8f6', a: .45 };
    if (y < rows * .45) return twinkle(x, y, t, '#e6edf8', .01);
  },
  sprites: (cols, rows, t) => {
    const c = cabinAt(cols, rows);
    return [
      { art: frame(CABIN_ARTS, t, 1.6), pal: CABIN_PAL, key: 1, x: c.x, y: c.y, shadow: '#0a1020',
        glow: { r: 7, color: '#ffb85c', a: .32 + .06 * Math.sin(t * 3) } },
      ...TREES.map(([fx, s], i) => ({ art: frame(PINES, t, .8, i * 1.3), pal: PINE_PAL, key: 10 + i, a: .6 + .4 * s,
        x: Math.round(cols * fx), y: groundAt(Math.round(cols * fx) + 4, rows) - 11 + Math.round((1 - s) * 3), halo: '#dfe8f6' })),
    ];
  },
};
