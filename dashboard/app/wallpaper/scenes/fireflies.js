// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, glyph } from '../kit'

const FLIES = [['a..', '.Y.', '..c'], ['..b', '.Y.', '...'], ['...', '.Y.', 'b.a']];
const FLY_PAL = { Y: { bg: '#e9f57a', fg: '#2c3308', a: 1 }, a: { ch: ':', fg: '#e9f57a', a: .55 }, b: { ch: "'", fg: '#e9f57a', a: .55 }, c: { ch: 'o', fg: '#e9f57a', a: .4 } };

export default {
  cell: (x, y, cols, rows, t) => {
    const h = 2 + Math.floor(hash(x, 7) ** 2 * 8);
    if (y < rows - h || (y < rows - 1 && hash(x, y, 8) > .75)) return;
    return { ch: glyph('SXt%S', x, y, t * .5), fg: '#4fb85c', a: .5 + .45 * (y - rows + h) / h };
  },
  sprites: (cols, rows, t) => Array.from({ length: 18 }, (_, i) => {
    const b = Math.max(0, Math.min(1, Math.sin(t * (.6 + .5 * hash(i, 5)) + i * 3) * 1.4 - .2));
    return { art: FLIES[i % 3], pal: FLY_PAL, key: i, a: .12 + .88 * b, glow: { r: 3.4, color: '#cfe86a', a: .18 + .3 * b },
      x: hash(i, 1) * cols + Math.sin(t * .2 * (1 + hash(i, 2)) + i) * 6 - 1,
      y: rows * (.08 + .65 * hash(i, 3)) + Math.cos(t * .17 * (1 + hash(i, 4)) + i * 2) * 3.5 - 1 };
  }),
};
