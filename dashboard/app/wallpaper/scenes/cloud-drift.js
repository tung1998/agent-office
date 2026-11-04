// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, drift, shape, frame } from '../kit'

const BIRDS = [['v'], ['^']], BIRD_PAL = { v: { ch: 'v', fg: '#3f6280', a: .8 }, '^': { ch: '^', fg: '#3f6280', a: .8 } };
const FLOCK = [[0, 0], [2, -1], [4, 0], [1.5, 1.5]];

const skies = new Map();
const skyArt = seed => {
  if (!skies.has(seed)) {
    const r = j => hash(seed, j, 3), kind = seed % 4;
    const [n, R0, R1, base] = [[3 + Math.floor(r(2) * 2), 2.4, 4.2, 1], [6 + Math.floor(r(2) * 3), 1.1, 1.8, 1],
      [2, 1.6, 2.4, 1], [3, 2.6, 5.2, 2]][kind];
    const domes = [];
    for (let i = 0, cx = 0; i < n; i++) {
      const mid = 1 - Math.abs(i - (n - 1) / 2) / Math.max(1, (n - 1) / 2), R = R0 + (R1 - R0) * (kind === 3 ? mid ** 2 : mid * .6 + r(10 + i) * .4);
      cx = i ? cx + R * .95 : R;
      domes.push([cx, R]);
      cx += R * .45;
    }
    const W = domes[n - 1][0] + domes[n - 1][1], lift = x => Math.max(0, ...domes.map(([cx, R]) => (Math.abs(x - cx) < R ? Math.sqrt(R * R - (x - cx) ** 2) * .75 : 0)));
    const H = Math.ceil(Math.max(...domes.map(d => d[1])) * .75 + base);
    skies.set(seed, shape(Math.ceil(W) + 1, H, (x, y) => {
      const up = H - 1 - y, h = Math.ceil(base + lift(x + .5) - .35);
      const end = x + .5 < domes[0][0] - domes[0][1] * .7 || x + .5 > domes[n - 1][0] + domes[n - 1][1] * .7;
      if (up >= h || (end && up >= base) || (up === h - 1 && h > 2 && hash(seed * 7 + x, 1, 4) < .25)) return '.';
      return String(1 + Math.min(3, Math.floor(4 * up / H)));
    }));
  }
  return skies.get(seed);
};
const DAY_TOPS = {
  4: { bg: '#ffffff', fg: '#9db3c6', a: .96 }, 3: { bg: '#f2f6fa', fg: '#93a9bd', a: .93 },
  2: { bg: '#e0e9f1', fg: '#869cb0', a: .9 }, 1: { bg: '#cad7e3', fg: '#7a90a4', a: .86 },
};
const WISP = ['~-~~.~~-~~~.-~', '..~~-~.~~~....'], WISP_PAL = { '~': { ch: '~', fg: '#ffffff', a: .55 }, '-': { ch: '-', fg: '#ffffff', a: .45 } };
const FAR = [[2, .1, .14, .8, .65], [5, .55, .08, .95, .6], [6, .8, .3, .75, .65], [9, .35, .3, .85, .6], [10, .95, .2, 1, .65]];
const NEAR = [[0, .15, .48, 1.6, 1], [3, .62, .42, 1.9, 1], [4, .88, .72, 1.7, 1], [7, .36, .78, 2.2, 1], [8, .05, .86, 1.5, 1]];
const CIRRUS = [[.2, .05, 2.6], [.7, .13, 2.2]];
const skySprite = (cols, rows, t) => ([seed, fx, fy, v, a]) => {
  const art = skyArt(seed), w = art[0].length;
  return { art, pal: DAY_TOPS, a, halo: '#ffffff', x: drift(cols, fx, t, v, w) - w / 2, y: fy * rows - art.length / 2, key: seed };
};

export default {
  sprites: (cols, rows, t) => [
    ...CIRRUS.map(([fx, fy, v], i) => ({ art: WISP, pal: WISP_PAL, key: 60 + i, x: drift(cols, fx, t, v, 14) - 7, y: fy * rows })),
    ...FAR.map(skySprite(cols, rows, t)),
    ...[0, 1].flatMap(f => FLOCK.map(([bx, by], j) => ({ art: frame(BIRDS, t, 3, j * .5), pal: BIRD_PAL, key: 20 + f * 4 + j,
      x: drift(cols, hash(f, 21), t, 2.4 + f * .4, 6) + bx, y: rows * (.25 + .25 * f) + Math.sin(t * .7 + f) * 1.5 + by }))),
    ...NEAR.map(skySprite(cols, rows, t)),
  ],
};
