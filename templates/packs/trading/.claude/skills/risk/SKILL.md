---
name: risk
description: Risk rules for the paper portfolio: position size from the stop, limits per position, sector and in total, the stop and target of every trade, and the daily mark-to-market. Use before opening or closing a paper trade, for the end-of-day check of open trades, and when the investor asks how much to put in.
---

# Risk

## Paper only
- Trades are written in the table `paper_trades`; nothing is ever sent to a broker or an exchange. No API keys of a broker or an exchange go in this repo, a chat or a memory. Asked to place a real order: say this office does paper trading only, and give the analysis.
- The rules below are the defaults; the investor's own are in `docs/risk-rules.md` and win.

## Sizing
- Paper capital: `docs/profile.md` (default 100,000,000 VND or 10,000 USD).
- Risk per trade: at most 1% of the capital, so `qty = capital × 1% / (entry − stop)`, rounded down to the market's lot (100 shares on HOSE/HNX).
- At most 20% of the capital in one position, 40% in one sector, 5 open positions, 100% invested (no margin, no short unless the profile allows it).
- Confidence 1-2: no trade (`watch`). Confidence 3: half size.

## A trade
- Open: only from a `signals` that went through the debate (skill `debate`), with entry, stop, target. Record `paper_trades`: `ticker`, `side`, `qty`, `entry_price` (the real price at that time, with its source; not the hoped one), `stop`, `target`, `signal` (the record's id), `opened_at`, `status` `open`, `reason`.
- The stop never moves away from the price; it may follow it (trailing) to lock in gains.
- Close: stop hit, target hit, the invalidation of the signal came true, or the horizon ended. `exit_price` (real, sourced), `closed_at`, `pnl`, `pnl_pct`, `r_multiple` = pnl / the risk at the open, `status` `closed`, `exit_reason`.
- Fees and tax count: 0.15% per side and 0.1% sales tax for VN stocks, 0.1% per side otherwise, unless the profile says other.

## End-of-day check
- For each open trade: the close price (sourced), unrealised P&L, stop or target hit during the day (by the day's low/high). Hit: close it at the stop/target price (a gap through the stop: at the open price).
- Portfolio: invested %, the biggest position and sector, the drawdown from the peak. Over a limit: say what to cut, do not cut it without the manager.
- Drawdown over 10% of the capital: no new trade until the weekly review (skill `review`) looked at why.
