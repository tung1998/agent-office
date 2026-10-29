---
name: debate
description: Bull/bear debate before a trading decision, as the researcher team of TradingAgents does: one side argues for, another against, each answering the other's points from the analysis, then the portfolio manager decides and writes down why. Use for every buy or sell signal before a paper trade, and when the investor asks "should I buy X".
---

# Bull and bear debate

## Why
One analyst tends to confirm the first idea. Two sides who must answer each other find what the report skipped. The bear side is run by another agent (another vendor's model when possible) than the one who wrote the analysis.

## Rounds
1. **Bull**: the 3 strongest reasons to buy, each from a number or a fact in the report (cite the section). No new data without a source.
2. **Bear**: the 3 strongest reasons not to, each from the report or from what it missed (a risk not looked at, stale data, a crowded trade, an event ahead). Answer each bull reason: holds, weak (why), or wrong (why).
3. **Bull answers** the bear's points the same way. At most 2 rounds; then stop, even without agreement.

## Decision (the portfolio manager)
- Sum up in a table: each point, its side, held or not after the debate.
- Decide: keep the signal, lower its confidence, change it (e.g. `buy` → `watch` at a level), or drop it. A bear point left unanswered lowers the confidence by at least 1.
- Write the debate and the decision at the end of the report (`## Debate`), and update the `signals` record (`signal`, `confidence`, `notes`).
- The decision is the office's, for paper trading; the investor makes any real one.
