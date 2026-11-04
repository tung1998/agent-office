// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, rising, sketch, frame, shade } from '../kit'

const jelly = ph => sketch(13, 22, [
  ...(ph ? [[0, 4, 'BBBBB'], [1, 2, 'BBBBBBBBB'], [2, 1, 'BBpBBBBBpBB'], [3, 0, 'RRRRRRRRRRRRR']]
    : [[0, 3, 'BBBBBBB'], [1, 1, 'BBBBBBBBBBB'], [2, 1, 'BBpBBBBBpBB'], [3, 1, 'RRRRRRRRRRR']]),
  [4, 3, 'kkkkkkk'],
  ...[1, 3, 5, 7, 9, 11].flatMap((c, k) => Array.from({ length: 16 }, (_, i) => {
    const r = hash(k, i, 7);
    return r < .18 ? [] : [[5 + i, c, r < .42 ? 'o' : 'l']];
  }).flat()),
]);
const JELLIES = [jelly(0), jelly(1)];
const jellyPal = c => ({
  B: { bg: c, fg: shade(c, -.6), a: .72 }, p: { bg: shade(c, .55), fg: c, a: .85 }, R: { bg: shade(c, .15), fg: shade(c, -.6), a: .78 },
  k: { bg: shade(c, -.15), fg: shade(c, -.65), a: .6 }, l: { bg: c, fg: shade(c, -.6), a: .62 }, o: { ch: 'o', fg: shade(c, .3), a: .5 },
});

const JELLY = [[.25, '#9a7fe0', .6, .7], [.82, '#ef8f63', .9, .95], [.06, '#ec92c6', .75, .9], [.55, '#7fb4ef', 1.1, .6]]
  .map(([x, c, v, a]) => [x, jellyPal(c), v, a]);

export default {
  cell: (x, y, cols, rows, t) => {
    const cx = -cols * .15, cy = -rows * .35, R = rows * .75 + cols * .1, d = Math.hypot(x - cx, y - cy);
    if (Math.abs(d - R) < .55 && (((Math.atan2(y - cy, x - cx) * R - t * 3) % 3) + 3) % 3 < 1) return { ch: 'o', fg: '#dbe8ff', a: .55 };
    if (Math.abs(d - R - 1.6) < .55 && hash(x, y, 71) < .4) return { ch: '~', fg: '#9cc8ff', a: .35 };
    if (rising(x, y, t, .6, .01)) return { ch: '.', fg: '#9cc8ff', a: .35 };
  },
  sprites: (cols, rows, t) => JELLY.map(([fx, pal, v, a], i) => {
    const span = rows + 34;
    return { art: frame(JELLIES, t, 1.1, i * .5), pal, a, key: i,
      x: fx * cols + Math.sin(t * .35 + i) * 2.5 - 6.5, y: rows + 4 - ((t * v + i * span / 4) % span) };
  }),
};
