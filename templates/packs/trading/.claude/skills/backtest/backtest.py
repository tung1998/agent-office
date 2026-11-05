#!/usr/bin/env python3
"""Backtest one simple long-only rule on daily OHLC prices, without look-ahead.
Standard library only. Paper research: it places no order anywhere.

  python3 .claude/skills/backtest/backtest.py PRICES.csv --rule sma-cross --fast 20 --slow 50
  python3 .claude/skills/backtest/backtest.py PRICES.csv --rule breakout --entry 20 --exit 10
  python3 .claude/skills/backtest/backtest.py PRICES.csv --rule rsi --period 14 --buy-below 30 --sell-above 55
  common: [--fee-bps 15] [--slippage-bps 10] [--stop-pct 8] [--from D] [--to D]
          [--split D (in-sample before D, out-of-sample from D)] [--capital 100000000]
          [--trades out.csv] [--json]

PRICES.csv: a header with date, open, high, low, close (any case; volume and
other columns ignored), one row per trading day, any order. Dates YYYY-MM-DD.

No look-ahead: a signal is computed from day t's close and earlier only, and
filled at day t+1's open, plus slippage and fees. A stop is checked on day t+1's
low (a gap below it fills at the open).
"""
import argparse, csv, json, math, sys
from datetime import date


def load(path):
    with open(path, newline="", encoding="utf-8-sig") as f:
        r = csv.DictReader(f)
        cols = {c.strip().lower(): c for c in (r.fieldnames or [])}
        need = ["date", "open", "high", "low", "close"]
        miss = [c for c in need if c not in cols]
        if miss:
            sys.exit(f"{path}: missing columns {miss}; header has {list(cols)}")
        rows = []
        for line in r:
            try:
                d = date.fromisoformat(line[cols["date"]].strip()[:10])
                o, h, l, c = (float(str(line[cols[k]]).replace(",", "")) for k in ("open", "high", "low", "close"))
            except (ValueError, TypeError):
                continue  # a bad or empty row
            if min(o, h, l, c) <= 0:
                continue
            rows.append((d, o, h, l, c))
    rows.sort()
    out = []
    for x in rows:  # one row per day
        if out and out[-1][0] == x[0]:
            out[-1] = x
        else:
            out.append(x)
    return out


def sma(vals, n, i):
    if i + 1 < n:
        return None
    return sum(vals[i + 1 - n:i + 1]) / n


def rsi_series(closes, n):
    out = [None] * len(closes)
    gain = loss = 0.0
    for i in range(1, len(closes)):
        ch = closes[i] - closes[i - 1]
        g, l = max(ch, 0.0), max(-ch, 0.0)
        if i <= n:
            gain += g
            loss += l
            if i == n:
                gain, loss = gain / n, loss / n
                out[i] = 100.0 if loss == 0 else 100 - 100 / (1 + gain / loss)
        else:
            gain = (gain * (n - 1) + g) / n
            loss = (loss * (n - 1) + l) / n
            out[i] = 100.0 if loss == 0 else 100 - 100 / (1 + gain / loss)
    return out


def signals(rows, a):
    """want[i] = True to be long after day i's close (uses data up to i only)."""
    closes = [r[4] for r in rows]
    highs = [r[2] for r in rows]
    lows = [r[3] for r in rows]
    want = [None] * len(rows)  # None = keep the position as it is
    if a.rule == "sma-cross":
        if a.fast >= a.slow:
            sys.exit("--fast must be below --slow")
        for i in range(len(rows)):
            f, s = sma(closes, a.fast, i), sma(closes, a.slow, i)
            if f is not None and s is not None:
                want[i] = f > s
    elif a.rule == "breakout":
        for i in range(len(rows)):
            if i >= a.entry and closes[i] > max(highs[i - a.entry:i]):
                want[i] = True
            elif i >= a.exit and closes[i] < min(lows[i - a.exit:i]):
                want[i] = False
    elif a.rule == "rsi":
        r = rsi_series(closes, a.period)
        for i, v in enumerate(r):
            if v is None:
                continue
            if v < a.buy_below:
                want[i] = True
            elif v > a.sell_above:
                want[i] = False
    return want


def run(rows, a):
    want = signals(rows, a)
    cost = (a.fee_bps + a.slippage_bps) / 10000.0
    cash, qty, entry_px, entry_day = a.capital, 0.0, 0.0, None
    equity, trades = [], []
    for i in range(len(rows)):
        d, o, h, l, c = rows[i]
        # fills happen at today's open, from yesterday's signal
        if i > 0:
            prev = want[i - 1]
            if qty == 0 and prev is True:
                px = o * (1 + cost)
                qty = cash / px
                cash = 0.0
                entry_px, entry_day = px, d
            elif qty > 0 and prev is False:
                px = o * (1 - cost)
                cash = qty * px
                trades.append((entry_day, d, entry_px, px, "signal"))
                qty = 0.0
            # stop-loss inside today's range
            if qty > 0 and a.stop_pct:
                stop = entry_px * (1 - a.stop_pct / 100.0)
                if l <= stop:
                    px = min(o, stop) * (1 - cost)
                    cash = qty * px
                    trades.append((entry_day, d, entry_px, px, "stop"))
                    qty = 0.0
                    want[i] = None if want[i] is True else want[i]  # no re-entry on the stop day's own signal
        equity.append((d, cash + qty * c, qty > 0))
    if qty > 0:  # mark the open trade at the last close (not a real exit)
        trades.append((entry_day, rows[-1][0], entry_px, rows[-1][4] * (1 - cost), "open"))
    return equity, trades


def stats(equity, trades, rows, capital):
    if len(equity) < 2:
        return {}
    days = (equity[-1][0] - equity[0][0]).days or 1
    years = days / 365.25
    end = equity[-1][1]
    peak, mdd = equity[0][1], 0.0
    for _, v, _ in equity:
        peak = max(peak, v)
        mdd = min(mdd, v / peak - 1)
    rets = [p / r - 1 for p, r in ((trades[k][3], trades[k][2]) for k in range(len(trades)))]
    closed = [x for x, t in zip(rets, trades) if t[4] != "open"]
    wins = [x for x in closed if x > 0]
    losses = [x for x in closed if x <= 0]
    bh_start, bh_end = rows[0][4], rows[-1][4]
    bpeak, bmdd = rows[0][4], 0.0
    for r in rows:
        bpeak = max(bpeak, r[4])
        bmdd = min(bmdd, r[4] / bpeak - 1)
    daily = [equity[k][1] / equity[k - 1][1] - 1 for k in range(1, len(equity))]
    mean = sum(daily) / len(daily)
    sd = math.sqrt(sum((x - mean) ** 2 for x in daily) / max(1, len(daily) - 1))
    return {
        "from": equity[0][0].isoformat(), "to": equity[-1][0].isoformat(), "days": len(equity),
        "total_return_pct": round((end / capital - 1) * 100, 2),
        "cagr_pct": round(((end / capital) ** (1 / years) - 1) * 100, 2) if years > 0 and end > 0 else None,
        "max_drawdown_pct": round(mdd * 100, 2),
        "sharpe_daily_rf0": round(mean / sd * math.sqrt(252), 2) if sd > 0 else None,
        "exposure_pct": round(100 * sum(1 for e in equity if e[2]) / len(equity), 1),
        "trades_closed": len(closed),
        "win_rate_pct": round(100 * len(wins) / len(closed), 1) if closed else None,
        "avg_win_pct": round(100 * sum(wins) / len(wins), 2) if wins else None,
        "avg_loss_pct": round(100 * sum(losses) / len(losses), 2) if losses else None,
        "profit_factor": round(sum(wins) / -sum(losses), 2) if losses and sum(losses) < 0 else None,
        "buy_hold_return_pct": round((bh_end / bh_start - 1) * 100, 2),
        "buy_hold_cagr_pct": round(((bh_end / bh_start) ** (1 / years) - 1) * 100, 2) if years > 0 else None,
        "buy_hold_max_drawdown_pct": round(bmdd * 100, 2),
    }


def window(rows, start, end):
    return [r for r in rows if (not start or r[0] >= start) and (not end or r[0] < end)]


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("prices")
    ap.add_argument("--rule", choices=["sma-cross", "breakout", "rsi"], required=True)
    ap.add_argument("--fast", type=int, default=20)
    ap.add_argument("--slow", type=int, default=50)
    ap.add_argument("--entry", type=int, default=20)
    ap.add_argument("--exit", type=int, default=10)
    ap.add_argument("--period", type=int, default=14)
    ap.add_argument("--buy-below", type=float, default=30)
    ap.add_argument("--sell-above", type=float, default=55)
    ap.add_argument("--fee-bps", type=float, default=15, help="fees per side in basis points (0.15%% = 15)")
    ap.add_argument("--slippage-bps", type=float, default=10)
    ap.add_argument("--stop-pct", type=float, default=0)
    ap.add_argument("--capital", type=float, default=100_000_000)
    ap.add_argument("--from", dest="from_")
    ap.add_argument("--to")
    ap.add_argument("--split")
    ap.add_argument("--trades")
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()

    rows = load(a.prices)
    end = date.fromisoformat(a.to) if a.to else None
    rows = [r for r in rows if (not a.from_ or r[0] >= date.fromisoformat(a.from_)) and (not end or r[0] <= end)]
    if len(rows) < 30:
        sys.exit(f"only {len(rows)} days of prices: too few to test anything")

    parts = [("all", rows)]
    if a.split:
        s = date.fromisoformat(a.split)
        parts += [("in_sample", window(rows, None, s)), ("out_of_sample", window(rows, s, None))]
    report = {"rule": a.rule, "params": {k: getattr(a, k) for k in ("fast", "slow", "entry", "exit", "period", "buy_below", "sell_above", "stop_pct")},
              "costs_bps_per_side": a.fee_bps + a.slippage_bps, "fill": "next day's open", "parts": {}}
    all_trades = []
    for name, part in parts:
        if len(part) < 30:
            report["parts"][name] = {"error": f"{len(part)} days: too few"}
            continue
        eq, tr = run(part, a)
        report["parts"][name] = stats(eq, tr, part, a.capital)
        if name == "all":
            all_trades = tr
    if a.trades:
        with open(a.trades, "w", newline="") as f:
            w = csv.writer(f)
            w.writerow(["entry_date", "exit_date", "entry_px", "exit_px", "return_pct", "exit_reason"])
            for t in all_trades:
                w.writerow([t[0], t[1], round(t[2], 4), round(t[3], 4), round((t[3] / t[2] - 1) * 100, 2), t[4]])
    if a.json:
        print(json.dumps(report, indent=2, default=str))
        return
    print(f"rule {a.rule} {report['params']}  costs {report['costs_bps_per_side']} bps/side, fills at next open")
    for name, s in report["parts"].items():
        print(f"\n[{name}]")
        for k, v in s.items():
            print(f"  {k:28} {v}")
    n = report["parts"].get("all", {}).get("trades_closed") or 0
    if n < 30:
        print(f"\nnote: {n} closed trades; under 30 the win rate and averages mean little")


if __name__ == "__main__":
    main()
