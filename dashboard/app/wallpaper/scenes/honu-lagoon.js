// Scene from ai-chat-themes by Aohkne (MIT): https://github.com/Aohkne/ai-chat-themes
import { SIDE, hash, drift, twinkle, rising, shape, flip, frame, shade } from '../kit'

const matrix = (x, y, cols, fg) => (x > cols * .82 && y % 2 === 0 && hash(x, y, 90) < .55 ? { ch: '-', fg, a: .14 } : undefined);

const REEF_COLORS = ["#2b9ee6","#53ddf9","#06aceb","#69ebfa","#e1f6f5","#21bbef","#3074ee","#007cef","#0499eb","#05befa","#1ccaf9","#01b5fa","#0281e1","#7c4d36","#033a9a","#40b9cd","#e5b77f","#01adf9","#0188f4","#946a62","#cbd5ac","#2561a5","#101b29","#b7a976","#35d4f9","#043a78","#01a0f9","#f4e9bf","#0163da","#01a6f8","#0191f8","#874fe2","#0142a9","#6e7cc9","#015ad2","#016ce4","#0198f8","#f3688c","#0488b8","#116c66","#9bf4f7","#0155ca","#014ec1","#f6cc38","#373b95","#5fc04f","#08508b","#0148b7","#ee7e3a","#163b5d","#a8a7e3","#3b9662","#555cba","#f6e5ab","#97df4f","#1f2d89"];
const REEF_W = 97, REEF_H = 47;
const REEF_THINGS = {
  turtle: { c: 24, r: 10, art: ['.........................', '.......ppjMMNN.....pKpK..', '.....pMMjnddjdj..pRPmRPN.', '...NMdndjddd4dddNRPkmDDp.', '..jdgdk44ddjmDPSgRDkRRR..', '.jndddddNPDDJJSSRrRRkp...', '..ddpPRRSSDDNSRrnnpC.....', '.DnRRRRgkDDDNrrRkPSSC....', '..KnngDDDDDngggp.DDJRp...', '.....kDkDPk.......NJSR...', '....PDPjDR.........nDPN..', '...RPDnRK...........gDN..', '..P..Rp..............pC..', '.........................'] },
  clownA: { c: 11, r: 20, art: ['........', '..dmMP..', '.M4M44M.', '.ddM4dM.', '........'] },
  clownB: { c: 16, r: 24, art: ['.........', '....pM...', '.LlMM4MC.', '.mM4MM4M.', '.........'] },
  tang: { c: 26, r: 27, art: ['.........', '...pp....', '.y6eeeFP.', '.66666pH.', '.........'] },
  yellow: { c: 48, r: 20, art: ['............', '........HP..', '.......HHHH.', '...DH...Hj..', '..HHHH......', '...HH...lHH.', '........HHH.', '............'] },
  crab: { c: 29, r: 42, art: ['BmgjM.', '.MMMdg', 'M..jM.', 'j.....'] },
  idolA: { c: 61, r: 19, art: ['.......', '....4..', '..mR...', '.4mrmH.', '.4m4m..', '..m4...', '.......'] },
  idolB: { c: 63, r: 25, art: ['........', '...4....', '..m.....', '.4m.mH..', '.rm4N...', '..m44...', '........'] },
  star: { c: 3, r: 35, art: ['............', '.....M......', '............', '..MMMMMMM...', '..MMMMHM....', '....MMMM....', '...MM..MM...', '............', '............'] },
  weedL: { c: 0, r: 10, art: ['........', '........', '.P......', '.JN.....', '..J.....', '...J....', '...J.D..', '..J..J..', '.JD.JJ..', '.JD.P.DN', '..PPD..S', '..JJ..JS', '.JP..DS.', '.PD.DS..', '.P.DJJ.S', '...P.P.S', '....D.JJ', '......J.', '........', '........', '........', '........', '........', '........'] },
  weedM: { c: 16, r: 31, art: ['..........', '..........', '..J.......', '..J..DJ...', '...J..S...', '..JJ.JP...', '..J.JP....', '..P.DD.D..', '...P.P.J..', '....D.PD..', '.....DP...'] },
  weedR: { c: 87, r: 17, art: ['..........', '..........', '..........', '..........', '......J.J.', '....J.PJJ.', '....J..JD.', '.....JJ.J.', '......J.J.', '......P...', '..........', '..........', '..........', '..........'] },
};
const REEF_KEYS = '0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ';
const REEF_PAL = Object.fromEntries(REEF_COLORS.map((c, i) => [REEF_KEYS[i], { bg: c, fg: shade(c, -.1), a: .96 }]));
const placeOf = (th, cols, rows) => [SIDE + th.c * (cols - SIDE) / REEF_W, th.r * rows / REEF_H];
const nudge = (art, pick, dx, dy) => {
  const g = art.map(r => [...r]), moved = [];
  art.forEach((row, y) => [...row].forEach((k, x) => { if (k !== '.' && pick(x, y, k)) { moved.push([x + dx, y + dy, k]); g[y][x] = '.'; } }));
  for (const [x, y, k] of moved) if (g[y]?.[x] !== undefined) g[y][x] = k;
  return g.map(r => r.join(''));
};
const parts = art => {
  const seen = new Set(), out = [];
  art.forEach((row, y) => [...row].forEach((k, x) => {
    if (k === '.' || seen.has(`${x},${y}`)) return;
    const cells = [], todo = [[x, y]];
    seen.add(`${x},${y}`);
    while (todo.length) {
      const [cx, cy] = todo.pop();
      cells.push([cx, cy]);
      for (const [nx, ny] of [[cx + 1, cy], [cx - 1, cy], [cx, cy + 1], [cx, cy - 1]])
        if ((art[ny]?.[nx] ?? '.') !== '.' && !seen.has(`${nx},${ny}`)) { seen.add(`${nx},${ny}`); todo.push([nx, ny]); }
    }
    const xs = cells.map(c => c[0]), ys = cells.map(c => c[1]), x0 = Math.min(...xs), y0 = Math.min(...ys);
    const own = shape(Math.max(...xs) - x0 + 1, Math.max(...ys) - y0 + 1, (px, py) => (cells.some(([a, b]) => a === px + x0 && b === py + y0) ? art[py + y0][px + x0] : '.'));
    if (cells.length > 2) out.push({ dx: x0, dy: y0, art: own });
  }));
  return out;
};
const wag = (art, facesRight) => {
  const w = art[0].length, tail = (x) => (facesRight ? x < w * .36 : x >= w * .64);
  return [art, nudge(art, x => tail(x), 0, -1), art, nudge(art, x => tail(x), 0, 1)];
};
const both = frames => [frames, frames.map(flip)];
const T0 = REEF_THINGS;
const REEF_FRAMES = {
  clownA: both(wag(T0.clownA.art, true)), clownB: both(wag(T0.clownB.art, true)),
  tang: both(wag(T0.tang.art, false)), idolA: both(wag(T0.idolA.art, false)), idolB: both(wag(T0.idolB.art, false)),
  yellow: parts(T0.yellow.art).map(p => ({ ...p, frames: wag(p.art, false) })),
  turtle: (() => {
    const a = T0.turtle.art, fl = (x, y) => y >= a.length * .64;
    return [a, nudge(a, fl, -1, 0), nudge(a, fl, -1, 1), nudge(a, fl, 0, 1)];
  })(),
  crab: (() => {
    const a = T0.crab.art, h = a.length;
    return [a, nudge(nudge(a, (x, y) => y === h - 1, 1, 0), (x, y) => y === 0, 0, -1), a, nudge(a, (x, y) => y === h - 1, -1, 0)];
  })(),
  star: (() => { const a = T0.star.art; return [a, a, a, nudge(a, (x, y) => y <= 1, 0, -1)]; })(),
};
const swaying = (art, t, i) => ({
  w: art[0].length + 4, h: art.length,
  field: (gx, gy) => {
    const off = Math.round(Math.sin(t * 1.2 + gy * .45 + i * 2) * 1.3 * (art.length - gy) / art.length), k = art[gy][gx - 2 - off];
    return k && k !== '.' ? { c: REEF_PAL[k], sx: gx - 2 - off, sy: gy } : undefined;
  },
});

export default {
  size: 18, // (the creatures keep the reference picture's scale)
  cell: (x, y, cols, rows, t) => {
    for (let i = 0; i < 9; i++)
      if (x === Math.floor(hash(i, 31) * cols) + Math.round(Math.sin(t + i))
        && y === Math.round(rows - ((t * 1.8 * (1 + hash(i, 32)) + hash(i, 33) * rows) % (rows + 4)))) return { ch: 'o', fg: '#e8fbff', a: .65 };
    if (rising(x, y, t, .5, .008)) return { ch: '.', fg: '#cdeefa', a: .35 };
    return twinkle(x, y, t, '#e8fbff', .006) ?? matrix(x, y, cols, '#e8fbff');
  },
  sprites: (cols, rows, t) => {
    const T = REEF_THINGS, F = REEF_FRAMES, out = [];
    const add = (art, x, y, key, extra = {}) => out.push({ art, pal: REEF_PAL, key, x, y, ...extra });
    const swim = (name, v, left) => {
      const w = T[name].art[0].length, [x] = placeOf(T[name], cols, rows), f = (x + w / 2 + w) / (cols + w * 2);
      const run = drift(cols, left ? 1 - f : f, t, v, w);
      return (left ? cols - run : run) - w / 2;
    };
    ['weedL', 'weedM', 'weedR'].forEach((n, i) => { const [x, y] = placeOf(T[n], cols, rows); out.push({ ...swaying(T[n].art, t, i), x: x - 2, y, key: 150 + i }); });
    const [sx, sy] = placeOf(T.star, cols, rows);
    add(frame(F.star, t, .6), sx, sy, 7);
    const [, ty] = placeOf(T.turtle, cols, rows);
    add(frame(F.turtle, t, 2.2), swim('turtle', .8, false), ty + Math.sin(t * .6) * .8, 1, { shadow: '#0b3d7a' });
    const [, yy] = placeOf(T.yellow, cols, rows), yx = swim('yellow', 1.3, true);
    F.yellow.forEach((p, i) => add(frame(p.frames, t, 4, i * 1.3), yx + p.dx + Math.sin(t * .9 + i * 2) * .8, yy + p.dy + Math.sin(t * 1.1 + i) * .7, 20 + i));
    const [, gy] = placeOf(T.tang, cols, rows);
    add(frame(F.tang[0], t, 5), swim('tang', 2, true), gy + Math.sin(t * 1.4) * .5, 3);
    ['clownA', 'clownB'].forEach((n, i) => {
      const [x, y] = placeOf(T[n], cols, rows), a = t * .7 + i * 2.2;
      add(frame(F[n][Math.cos(a) < 0 ? 1 : 0], t, 5, i * 2), x + Math.sin(a) * 2.5, y + Math.sin(t * 1.3 + i) * .6, 4 + i);
    });
    ['idolA', 'idolB'].forEach((n, i) => {
      const [x, y] = placeOf(T[n], cols, rows), a = t * .45 + i * 1.7;
      add(frame(F[n][Math.cos(a) > 0 ? 1 : 0], t, 3, i * 2), x + Math.sin(a) * 1.5, y + Math.sin(t * .8 + i) * .8, 8 + i);
    });
    const [cx, cy] = placeOf(T.crab, cols, rows);
    add(frame(F.crab, t, 6), cx + Math.sin(t * .9) * 4, cy, 6);
    return out;
  },
};
