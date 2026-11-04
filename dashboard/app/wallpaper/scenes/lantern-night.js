// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { hash, drift, near, twinkle, frame } from '../kit'

const LANTERN_NEAR = ['..RRRR..', '.RRRRRR.', 'RRRRRRRR', '.pPPPPp.', '.pPPKPp.', '.pPKKPp.', '.pPPPPp.', '.pPPPPp.', '.WWWWWW.', 'wwwwwwww', '.wwwwww.'];
const LANTERN_FAR = ['RRRRR', '.pPp.', '.pKp.', '.WWW.', 'wwwww'];
const flicker = art => art.map((r, y) => (y === 3 || y === art.length - 4 ? r.replace(/P/g, 'p') : r));
const REFLECT_NEAR = [['..rrrr..', '...rr...', '..r..r..', '...rr...', '....r...'], ['.rrrrr..', '..rr.r..', '...rr...', '..r.....', '...r....'], ['..rrrrr.', '..r.rr..', '...rr...', '.....r..', '....r...']];
const REFLECT_FAR = [['.rrr.', '..r..', '.r...'], ['rrr..', '.r.r.', '..r..'], ['.rrrr', '..r..', '...r.']];
const lanternArts = (art, reflects) => [art, flicker(art)].flatMap(a => reflects.map(r => [...a, ...r]));
const LANTERN_ARTS = [lanternArts(LANTERN_FAR, REFLECT_FAR), lanternArts(LANTERN_NEAR, REFLECT_NEAR)];
const LANTERN_PAL = {
  R: { bg: '#3a1a14', fg: '#170905', a: .95 }, P: { bg: '#ffd889', fg: '#c46a1c', a: .97 }, p: { bg: '#f4a347', fg: '#7a3a0c', a: .95 },
  K: { bg: '#e07a2a', fg: '#5a2a08', a: .95 }, W: { bg: '#4a2e1c', fg: '#1e120a', a: .95 }, w: { bg: '#2e1c12', fg: '#120a06', a: .92 },
  r: { bg: '#f6b04e', fg: '#7a3a0c', a: .4 },
};
const LANTERNS = [[.05, .1, .12], [.4, .05, .1], [.75, .18, .14], [.2, .35, .2], [.6, .42, .18], [.9, .3, .22], [.12, .72, .3], [.5, .85, .26], [.82, .68, .32]];
const BANK = .3;
const lantern = (cols, rows, t, i, [fx, depth, v]) => {
  const near = depth > .5, art = frame(LANTERN_ARTS[+near], t, 2.2, i * 1.7);
  const top = rows * BANK + 1 + depth * (rows * (1 - BANK) - 16);
  return { art, pal: LANTERN_PAL, key: 60 + i, a: .7 + .3 * depth, glow: { r: near ? 7 : 4, color: '#ffb347', a: near ? .42 : .28 },
    x: drift(cols, fx, t, v, art[0].length), y: top + Math.sin(t * .8 + i * 2) * .5 };
};

export default {
  cell: (x, y, cols, rows, t) => {
    const bank = Math.round(rows * BANK), roof = bank - 1 - Math.floor(2.6 * (.5 + .5 * Math.sin(x * .21)) + hash(Math.floor(x / 3), 7) * 2.2);
    if (y < roof) return twinkle(x, y, t, '#fff1cf', .012);
    if (y < bank) {
      if (y < bank - 1 && hash(x, y, 41) < .07) return { bg: '#f2b65a', fg: '#6a3a0c', a: .55 + .3 * Math.sin(t * .7 + x) ** 2 };
      return { bg: '#04070e', fg: '#131c30', a: .97 };
    }
    if (y < bank + 4 && hash(x, bank - 2, 41) < .07 && hash(x, y + Math.floor(t * 2), 43) < .55) return { ch: '|', fg: '#f2b65a', a: .35 * (1 - (y - bank) / 4) };
    if (hash(Math.floor(x / 4 + t * .25 * (1 + (y % 3) * .3)), y, 45) < .07) return { ch: (x + y) % 2 ? '~' : '-', fg: '#8fb0d6', a: .16 };
  },
  sprites: (cols, rows, t) => LANTERNS.map((l, i) => lantern(cols, rows, t, i, l)),
};
