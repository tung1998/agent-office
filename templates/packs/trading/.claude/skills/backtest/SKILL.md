---
name: backtest
description: Test a simple trading rule (moving-average cross, breakout, RSI) on daily prices before trusting it, with the script backtest.py of this skill: real fees and slippage, fills at the next day's open (no look-ahead), drawdown, win rate, CAGR, against buy-and-hold, in-sample vs out-of-sample. Use when the investor asks "does this strategy work", before a rule goes into docs/risk-rules.md or the analysis, and for #backtest-strategy.
---

# Backtest

A backtest says how a rule would have done on past prices. It does not say it will work next: it is evidence against a bad rule more than proof of a good one.

## 1. The prices
- Daily OHLC from a free source in `sources/data-sources.md` (an exchange's or broker's public history page, Stooq, Yahoo Finance download, the exchange's official data); no paid data, no login. Save it as `analysis/data/<TICKER>-daily.csv` with a header `date,open,high,low,close[,volume]`, and write the source, the date downloaded and whether prices are adjusted for splits and dividends in the `backtests` record.
- At least 3 years when the rule allows (two full market moods); fewer: say so.
- Prices not adjusted around a split will fake a crash: check the biggest one-day moves against the news.

## 2. Run it
```
python3 .claude/skills/backtest/backtest.py analysis/data/FPT-daily.csv --rule sma-cross --fast 20 --slow 50 --split 2024-01-01 --trades analysis/backtests/<date>-FPT-sma.csv
python3 .claude/skills/backtest/backtest.py <csv> --rule breakout --entry 20 --exit 10 --stop-pct 8
python3 .claude/skills/backtest/backtest.py <csv> --rule rsi --period 14 --buy-below 30 --sell-above 55
```
- Costs: `--fee-bps` per side (the market's real fee and tax; Vietnam stocks: about 0.15% fee + 0.1% sell tax → `--fee-bps 20`), `--slippage-bps` (10 for liquid, 30+ for thin stocks and small coins).
- `--split <date>`: tune on the in-sample part only, judge on the out-of-sample part. Always use it when parameters were chosen by trying.
- `--json` for the numbers in a machine-readable form.
- The script cannot run (no Python, the command not allowed): say so; never make the numbers up.

## 3. Read it honestly
- Against buy-and-hold: a rule that returns less than holding, with a similar drawdown, is not worth trading.
- Drawdown and exposure first, return second; CAGR over a short period means little.
- Under 30 closed trades: the win rate and averages are noise; say so.
- Overfitting signs: many parameters tried and the best one reported; out-of-sample much worse than in-sample; the result flips with a small change of a parameter (try ±25%); it only works in one period. Report how many variants were tried.
- Survivorship: testing only on stocks that are still listed and went up flatters the rule.

## 4. Record (`backtests`)
One record per run worth keeping: `date`, `ticker`, `rule`, `params`, `period`, `data_source`, `variants_tried`, the numbers (all, in-sample, out-of-sample), `vs_buy_hold`, `verdict` (`keep`, `reject`, `inconclusive`), `report` (the file). A rule kept goes to the investor as a proposed line for `docs/risk-rules.md` or the skill `analysis`; nothing changes without their yes.

## Limits
Paper research only: a backtest never becomes a real order.
