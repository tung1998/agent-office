// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { SIDE, hash, ell, twinkle, shade } from '../kit'

const CARP_R = [1.8, 2.8, 3.4, 3.8, 3.9, 3.9, 3.7, 3.4, 3, 2.6, 2.2, 1.8, 1.4, 1.1, .9, .8], CARP_LEN = CARP_R.length + 5;
const CARP_PAL = (() => {
  const p = {
    w: { bg: '#f3eee5', fg: '#a59f94', a: .95 }, r: { bg: '#de3a2a', fg: '#6e140c', a: .95 }, k: { bg: '#1f1c1b', fg: '#6a6560', a: .95 },
    g: { bg: '#f2b52c', fg: '#7a5208', a: .95 }, l: { bg: '#f8d769', fg: '#8a6110', a: .95 }, e: { bg: '#120c0a', fg: '#5a504a', a: .97 },
    f: { bg: '#e6e2d8', fg: '#9a948a', a: .78 }, F: { bg: '#f5cc4f', fg: '#8a6110', a: .8 },
  };
  for (const k of 'wrkgl') p[k.toUpperCase() + '_'] = { ...p[k], bg: shade(p[k].bg, -.16) };
  return p;
})();
const CARP_KINDS = [
  { base: 'w', fin: 'f', spots: [[2, .6, 1.3, 'r'], [5.5, 1.6, 1.9, 'r'], [9.5, -1.3, 1.8, 'r'], [13, .8, 1.2, 'r']] },
  { base: 'w', fin: 'f', spots: [[1, 0, 1.5, 'r'], [3.5, -1.6, 1.9, 'k'], [6, 1.2, 2.2, 'r'], [8, 2, 1.6, 'k'], [10, -1, 2, 'r'], [12, -.6, 1.6, 'k'], [14.5, .4, 1.2, 'k']] },
  { base: 'g', fin: 'F', spots: [] },
];
const CARP_LOOPS = [[.74, .3, .15, .17, 1, 1], [.26, .42, .13, .24, 1, -1], [.56, .66, .2, .12, 1, -1]];
const carpPath = (cols, rows, i, s) => {
  const [mx, my, rx, ry, k, dir] = CARP_LOOPS[i];
  return [SIDE + (cols - SIDE) * (mx + rx * Math.cos(s * k * dir + i * 2)), rows * (my + ry * Math.sin(s * dir + i * 2))];
};
const carp = (cols, rows, t, i) => {
  const z = Math.min(.75, Math.max(.5, (cols - SIDE) / 90));
  let s = t * (.06 + i * .01);
  const spine = [carpPath(cols, rows, i, s)];
  for (let n = 0; spine.length < CARP_LEN && n < 20000; n++) {
    const q = carpPath(cols, rows, i, s -= .002), [lx, ly] = spine[spine.length - 1];
    if (Math.hypot(q[0] - lx, q[1] - ly) >= z) spine.push(q);
  }
  const norm = spine.map((_, k) => {
    const [ax, ay] = spine[Math.max(0, k - 1)], [bx, by] = spine[Math.min(spine.length - 1, k + 1)], d = Math.hypot(ax - bx, ay - by) || 1;
    return [-(ay - by) / d, (ax - bx) / d];
  });
  const xs = spine.map(p => p[0]), ys = spine.map(p => p[1]), ox = Math.min(...xs) - 6, oy = Math.min(...ys) - 6;
  const kind = CARP_KINDS[i], P = CARP_PAL;
  const swish = u => Math.sin(t * 3.2 + i * 2) * 1.3 * Math.max(0, u - 9) / 11;
  const paddle = 4.8 + .7 * Math.sin(t * 2.4 + i);
  return {
    x: ox, y: oy, w: Math.ceil(Math.max(...xs) - ox + 7), h: Math.ceil(Math.max(...ys) - oy + 7), key: i, shadow: '#0b3430',
    field: (gx, gy) => {
      const wx = ox + gx + .5, wy = oy + gy + .5;
      let u = 0, d = Infinity;
      spine.forEach(([px, py], j) => { const e = Math.hypot(wx - px, wy - py); if (e < d) { d = e; u = j; } });
      d /= z;
      const [nx, ny] = norm[u], dx = (wx - spine[u][0]) / z, dy = (wy - spine[u][1]) / z;
      const v = dx * nx + dy * ny - swish(u), av = Math.abs(v), seed = { sx: u + i * 40, sy: Math.round(v * 1.5) };
      if (u < CARP_R.length && av <= CARP_R[u] && d <= CARP_R[u] + 1) {
        if (u >= 1 && u <= 2 && Math.abs(av - 1.8) < .55) return { c: P.e, ...seed };
        const spot = kind.spots.find(([su, sv, sr]) => Math.hypot(u - su, v - sv) < sr);
        let key = spot ? spot[3] : kind.base;
        if (kind.base === 'g' && av < .9 && u > 1) key = 'l';
        return { c: av > CARP_R[u] - .8 ? P[key.toUpperCase() + '_'] : P[key], ...seed };
      }
      if (u >= CARP_R.length) {
        const a = u - CARP_R.length + (u === spine.length - 1 ? Math.max(0, dy * nx - dx * ny) : 0);
        if (av <= .7 + Math.min(a, 4.5) * .5 && a <= 4 + av * .8 && !(a > 4 && av < (a - 4) * 1.4 + .5)) return { c: P[kind.fin], ...seed };
      }
      if (u >= 3 && u <= 8 && ((u - 5.5) / 2.2) ** 2 + ((av - paddle) / 1.6) ** 2 <= 1) return { c: P[kind.fin], ...seed };
      if (u >= 9 && u <= 12 && ((u - 10.5) / 1.3) ** 2 + ((av - 3) / 1.1) ** 2 <= 1) return { c: P[kind.fin], ...seed };
    },
  };
};
const LILIES = [[.8, .06, 4.2, 2.4], [.95, .14, 6.5, 2], [.99, .42, 5, 2.8], [.01, .55, 5.5, .2], [.2, .97, 4.8, -1.6], [.04, .95, 6.5, -.9], [.97, .95, 5.5, -2.2]];
const LOTUS = [[.82, .08, 3.2], [.18, .87, 3.2]];
const LILY_PAL = {
  pad: { bg: '#5c9a43', fg: '#2c5520', a: .93 }, rim: { bg: '#3f7a31', fg: '#1d3d15', a: .93 }, vein: { bg: '#86c066', fg: '#3a6a2a', a: .93 },
  petal: { bg: '#f28bb8', fg: '#8a2d55', a: .95 }, inner: { bg: '#f9c3da', fg: '#a2466e', a: .95 }, heart: { bg: '#f6d03f', fg: '#7a5a08', a: .97 },
  shadow: { bg: '#0b3430', fg: '#0b3430', ch: '', a: .35 },
};
const lilyAt = (x, y, cols, rows, t) => {
  const W = cols - SIDE;
  for (const [fx, fy, r] of LOTUS) {
    const cx = SIDE + fx * W, cy = fy * rows, dx = x + .5 - cx, dy = y + .5 - cy, d = Math.hypot(dx, dy), th = Math.atan2(dy, dx);
    const p = r * (.55 + .45 * Math.abs(Math.cos(4 * th + fx * 9))) * (1 + .07 * Math.sin(t * .8 + fx * 5));
    if (d < .9) return LILY_PAL.heart;
    if (d < p * .55) return LILY_PAL.inner;
    if (d < p) return LILY_PAL.petal;
  }
  for (const [fx, fy, r, notch] of LILIES) {
    const cx = SIDE + fx * W, cy = fy * rows, dx = x + .5 - cx, dy = y + .5 - cy, d = Math.hypot(dx, dy);
    if (d > r) continue;
    const th = Math.atan2(dy, dx), off = Math.abs(((th - notch) % (Math.PI * 2) + Math.PI * 3) % (Math.PI * 2) - Math.PI);
    if (off < .3 && d > .4) continue;
    if (d > r - .8) return LILY_PAL.rim;
    const spoke = Math.abs(((((th - notch) * 11 / (Math.PI * 2)) % 1) + 1) % 1 - .5);
    return d < .8 || (d > 1.2 && spoke > .5 - .35 / d) ? LILY_PAL.vein : LILY_PAL.pad;
  }
};

export default {
  cell: (x, y, cols, rows, t) => {
    const lily = lilyAt(x, y, cols, rows, t);
    if (lily) return lily;
    if (lilyAt(x - 1, y - 1, cols, rows, t)) return LILY_PAL.shadow;
    if (hash(x - Math.floor(t * .3), y + Math.floor(t * .12), 77) < .004) return { bg: '#f4b6cc', fg: '#8a2d55', a: .85 };
    for (let i = 0; i < 5; i++) {
      const rr = (t * 1.6 + i * 3.7) % 10, d = Math.hypot(x - (SIDE + hash(i, 61) * (cols - SIDE)), y - hash(i, 62) * rows);
      if ((Math.abs(d - rr) < .5 || Math.abs(d - rr + 2.2) < .5) && hash(x, y, 63) < .7) return { ch: 'o', fg: '#d6f1e8', a: .4 * (1 - rr / 10) };
    }
    const deep = ell(x, y, SIDE + (cols - SIDE) * .5, rows * .45, (cols - SIDE) * .2, rows * .28);
    if (deep < 1) return { bg: '#0d3a38', fg: '#0d3a38', ch: '', a: .45 * (1 - deep) };
    if (hash(x, y, 71) < .03) return { ch: 'abcdeghkmnopqtu_.*'[Math.floor(hash(x, y, 72) * 18)], fg: '#cfeee6', a: .2 };
    return twinkle(x, y, t, '#e8fbf6', .004);
  },
  sprites: (cols, rows, t) => [0, 1, 2].map(i => carp(cols, rows, t, i)),
};
