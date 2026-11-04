// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, twinkle, rising, flip, frame } from '../kit'

// Two cherry branches reach in from the top corners; petals tumble down; hills and a low sun sit at the bottom.
const BRANCH = [
  'BBBBBBBBBBBBbbb.........FF..',
  'BBBBBbbbFFf...bbbb....FFcFF.',
  'BBBb..FFcFF......bbbbb.FF...',
  'BBb....FFf...fFF....bb......',
  'Bb..........FFcF.....bFFf...',
  'B.......FF...fFF....FFcFF...',
  'b......FcFF..........fFF....',
  'b.......Ff..................',
];
// The blossoms breathe: pale and full petals trade places, a few frames each
const bloom = (art, k) => art.map(r => r.replace(/[Ff]/g, (c, i) => ((i + k) % 3 ? c : c === 'F' ? 'f' : 'F')));
const BRANCHES = [0, 1, 2].map(k => bloom(BRANCH, k));
const BRANCHES_R = BRANCHES.map(flip);
const BRANCH_PAL = {
  B: { bg: '#2a1a24', fg: '#120a10', a: .97 }, b: { ch: '=', fg: '#3e2834', a: .9 },
  F: { bg: '#f7a8c4', fg: '#9a3d62', a: .95 }, f: { bg: '#ffd6e4', fg: '#b8607e', a: .9 },
  c: { bg: '#ffe9a8', fg: '#9a5a20', a: .95 },
};
const branch = (cols, rows, t, right) => {
  const art = frame(right ? BRANCHES_R : BRANCHES, t, 1.4, right ? 1 : 0);
  return { art, pal: BRANCH_PAL, key: right ? 2 : 1, halo: '#f7a8c4', shadow: true,
    glow: { r: 5, color: '#f7a8c4', a: .16 },
    x: right ? cols - art[0].length : 0, y: Math.sin(t * .5 + (right ? 2 : 0)) * .4 };
};

// A petal turns as it falls: flat, edge-on, flat the other way
const PETALS = [['Pp'], ['P', '.'], ['pP'], ['.', 'P']];
const PETAL_PAL = { P: { bg: '#f7a8c4', fg: '#9a3d62', a: .9 }, p: { ch: "'", fg: '#ffd6e4', a: .7 } };
const petal = (cols, rows, t, i) => {
  const v = .8 + 1.2 * hash(i, 1), span = rows + 4, y = ((hash(i, 2) * span + t * v) % span) - 2;
  const side = hash(i, 3) < .5, depth = hash(i, 4);
  const x0 = side ? cols * (.02 + .3 * hash(i, 5)) : cols * (.68 + .3 * hash(i, 5));
  return { art: frame(PETALS, t, 2 + 2 * hash(i, 6), i), pal: PETAL_PAL, key: 10 + i, a: .35 + .6 * depth,
    x: x0 + y * .35 + Math.sin(t * .9 + i) * 3, y };
};

export default {
  cell: (x, y, cols, rows, t) => {
    const hill = rows - 3 - Math.round(2.5 * (.5 + .5 * Math.sin(x * .09 + 1)) + 1.5 * (.5 + .5 * Math.sin(x * .23)));
    const sx = cols * .8, sy = hill + 1, d = Math.hypot((x - sx) * .5, y - sy);
    if (y >= hill) {
      if (hash(x, y, 21) < .05) return { ch: '.', fg: '#f7a8c4', a: .45 };
      return { bg: '#1a1222', fg: '#2c1d34', a: .96 };
    }
    if (d < 4) return { bg: '#ffb48a', fg: '#c0603a', a: .75, floor: .3 };
    if (d < 6 && hash(x, y, 22) < .4) return { fg: '#ffb48a', a: .25 + .15 * Math.sin(t + x) };
    if (y < rows * .45) return twinkle(x, y, t, '#f3dcef', .012);
    if (rising(x, y, -t, .6, .006)) return { ch: '.', fg: '#ffd6e4', a: .25 };
  },
  sprites: (cols, rows, t) => [
    branch(cols, rows, t, false), branch(cols, rows, t, true),
    ...Array.from({ length: Math.round(8 + cols / 8) }, (_, i) => petal(cols, rows, t, i)),
  ],
};
