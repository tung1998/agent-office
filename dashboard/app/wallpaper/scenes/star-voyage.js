// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, ell, drift, near, twinkle, sketch, flip, frame, shade } from '../kit'

// Star Voyage: a ringed planet in the top right, a rocket that flies across the top with a puffing trail,
// a small moon that circles the planet, shooting stars, and twinkling stars and slow dust behind.

// The rocket points right. Its flame flickers through three frames.
const HULL = [
  [0, 3, 'W'],
  [1, 3, 'WBBBBb'],
  [2, 3, 'WBBoBBBBNn'],
  [3, 3, 'WBBBBb'],
  [4, 3, 'W'],
];
const FLAMES = [
  [[2, 0, 'fFF']],
  [[1, 1, 'f'], [2, 0, 'FFF'], [3, 1, 'f']],
  [[2, 1, 'fF'], [1, 2, 'f']],
];
const ROCKET = FLAMES.map(f => sketch(13, 5, [...HULL, ...f]));
const ROCKET_PAL = {
  B: { bg: '#dfe4f5', fg: '#6a7090', a: .95 }, b: { bg: '#aab1cc', fg: '#4a5070', a: .95 },
  W: { bg: '#ff6fb5', fg: '#5a1838', a: .95 }, o: { bg: '#4fe3ff', fg: '#0d3a4a', a: .95 },
  N: { bg: '#ff6fb5', fg: '#5a1838', a: .95 }, n: { ch: '>', fg: '#ff6fb5', a: .8 },
  F: { bg: '#ffb347', fg: '#7a3a00', a: .9 }, f: { ch: '~', fg: '#ff7a3d', a: .8 },
};
const SPEED = 4, ROCKET_W = 13;
const rocketAt = (cols, rows, t) => ({
  x: drift(cols, .3, t, SPEED, ROCKET_W),
  y: rows * .12 + 1.5 * Math.sin(t * .9),
});

// A ringed planet, sized by the window's width and lit from the upper left.
const planetOf = (cols, rows) => {
  const R = Math.max(4, Math.min(9, cols * .065));
  return { cx: cols * .84, cy: rows * .32, R };
};
const BANDS = ['#7a4cc2', '#9b5fd6', '#c76fd0', '#8a55c8', '#6a3fae'];
const planet = (x, y, t, { cx, cy, R }) => {
  const dx = x + .5 - cx, dy = y + .5 - cy, d = Math.hypot(dx, dy);
  const ring = ell(x + .5, y + .5, cx, cy + dx * .12, R * 2.1, R * .5);
  const onRing = ring > .78 && ring < 1.05, front = dy + dx * .12 > 0;
  if (onRing && (front || d > R)) {
    const a = .55 + .25 * Math.sin(dx * .6 - t * 1.5);
    return { bg: '#e6c9ff', fg: '#5a3a80', a: d > R ? a * .8 : a, floor: .45 };
  }
  if (d <= R) {
    const band = BANDS[Math.floor(((dy / R + 1) * 2.5 + .4 * Math.sin(dx * .5 + t * .3))) % BANDS.length] ?? BANDS[0];
    const lit = (-dx - dy) / (R * 1.6);
    return { bg: shade(band, Math.max(-.55, Math.min(.25, lit))), fg: shade(band, -.6), a: .92, floor: .5 };
  }
  if (onRing || d <= R + 1.5) return near(x, y, '#c9a6ff', .22, .35);
};

// Shooting stars now and then, across the upper sky.
const STREAK = sketch(8, 4, [[0, 0, '11'], [1, 2, '22'], [2, 4, '33'], [3, 6, '4H']]);
const STREAKS = [STREAK, flip(STREAK)];
const streakPal = ch => ({
  H: { bg: '#fff3fb', fg: '#5a3a50', a: 1 }, 4: { ch, fg: '#ffd1ea', a: .85 }, 3: { ch, fg: '#ffd1ea', a: .6 },
  2: { ch, fg: '#e6ecff', a: .4 }, 1: { ch, fg: '#e6ecff', a: .2 },
});
const STREAK_PALS = [streakPal('\\'), streakPal('/')];
const EVERY = 9, FALL = 1.3;
const shooting = (cols, rows, t) => {
  const k = Math.floor(t / EVERY), p = (t - k * EVERY) / FALL;
  if (p >= 1) return [];
  const left = hash(k, 3) < .5, run = 24 * p;
  const hx = cols * (.2 + .6 * hash(k, 1)) + (left ? -run : run), hy = rows * (.3 + .3 * hash(k, 2)) + run * .5;
  return [{ art: STREAKS[+left], pal: STREAK_PALS[+left], key: 40, a: Math.sin(Math.PI * p), x: left ? hx : hx - 7, y: hy - 3,
    glow: { r: 5, color: '#ffd1ea', a: .3 * Math.sin(Math.PI * p) } }];
};

const MOON = ['.mm.', 'mMMm', '.mm.'];
const MOON_PAL = { M: { bg: '#d8dcec', fg: '#5a6080', a: .95 }, m: { bg: '#a8aec8', fg: '#4a5070', a: .9 } };

export default {
  cell: (x, y, cols, rows, t) => {
    const p = planet(x, y, t, planetOf(cols, rows));
    if (p) return p;
    // The rocket's trail: puffs left behind where it was a moment ago, fading with distance.
    const r = rocketAt(cols, rows, t), d = r.x - ROCKET_W / 2 - x;
    if (d > 0 && d < 16) {
      const ty = rocketAt(cols, rows, t - d / SPEED).y + 2;
      if (Math.abs(y + .5 - ty - .5) < .7 && hash(x, y, Math.floor(t * 5)) < .55 - d * .025)
        return { ch: d < 5 ? 'o' : '.', fg: d < 5 ? '#ffb347' : '#ffd1ea', a: .6 * (1 - d / 16) };
    }
    // Slow dust that drifts left, and twinkling stars.
    if (hash(Math.floor(x + t * .8), y, 17) < .004) return { ch: '.', fg: '#9aa6d8', a: .35 };
    return twinkle(x, y, t, '#e6ecff', .018);
  },
  sprites: (cols, rows, t) => {
    const { cx, cy, R } = planetOf(cols, rows), r = rocketAt(cols, rows, t), m = t * .35;
    const moonBehind = Math.sin(m) < 0 && Math.abs(Math.cos(m) * R * 2.8) < R + 2;
    return [
      ...shooting(cols, rows, t),
      { art: ['.'], pal: {}, x: cx - .5, y: cy - .5, key: 1, glow: { r: R * 2.2, color: '#9b5fd6', a: .22 } },
      ...(moonBehind ? [] : [{ art: MOON, pal: MOON_PAL, key: 2, shadow: '#05040d', halo: '#c9cde0',
        x: cx + Math.cos(m) * R * 2.8 - 2, y: cy + Math.sin(m) * R * .9 - 1.5 }]),
      { art: frame(ROCKET, t, 8), pal: ROCKET_PAL, key: 3, shadow: '#05040d', x: r.x - ROCKET_W / 2, y: r.y,
        glow: { r: 5, color: '#ffb347', a: .3 + .1 * Math.sin(t * 13) } },
    ];
  },
};
