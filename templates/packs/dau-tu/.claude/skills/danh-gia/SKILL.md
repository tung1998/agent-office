---
name: danh-gia
description: Review past signals and paper trades against what the price did, score the analysts' calls, and keep lessons as tagged records the next analysis reads (the reflection loop of FinMem and TradingAgents). Use for the weekly review, when a signal reaches its review date, or when the investor asks how the office has been doing.
---

# Review and lessons

## 1. Signals due
- `tin_hieu` with `status` `mo` and `review_on` up to today. For each: the price now (sourced), the move since `price`, and whether the stop or the target came first (from the daily highs/lows).
- Outcome: `dung` (right: the target, or the move in the signal's direction more than the market's), `sai` (wrong: the stop, or the move against it), `hoa` (neither, within ±2% of the market). `theo-doi`: right when the level waited for was the right call.
- Update the record: `outcome`, `result_pct`, `status` `da-danh-gia`.

## 2. Scores (in `nhat-ky/<YYYY>-W<week>.md`)
- Paper portfolio: value, P&L of the week and since the start, against the benchmark of the profile (VN-Index, S&P 500, BTC), max drawdown, win rate, average R of closed trades.
- Signals: right/wrong/even by `signal`, by confidence (are 4-5 right more often than 3?) and by the view that drove them (tag `ly-do:*`).
- Under 20 signals reviewed: say the numbers mean little yet.

## 3. Lessons (`bai_hoc`)
- From each wrong signal and each loss over 1R: what was missed, at which step (data, a view, the debate, the sizing), and the rule that would have caught it. One record: `date`, `lesson` (one sentence, a rule to follow next time), `context`, `tags` (ticker, sector, `buoc:<step>`, `ly-do:*`), `source` (the signal's id), `status` `moi`.
- Search `bai_hoc` first: the same lesson again = `count` + 1, not a new record. A lesson that held 3 times = `status` `quy-tac`; propose adding it to `docs/quy-tac-rui-ro.md` or to the skill it belongs to, for the investor to agree.
- A lesson shown wrong later: `status` `bo`, with why.
- Right signals teach too: what worked, when it worked more than once.
