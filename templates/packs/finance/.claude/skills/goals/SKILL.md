---
name: goals
description: Track saving goals and debts (table goals): current amount, the monthly amount needed to make the date, payoff order for debts, options when off track. Use when the person sets a goal, asks if they can afford something, or at the monthly goal check.
---

# Goals and debts

## A goal (`goals`)
- `kind` `saving`: `target`, `current` (what is set aside: an account's balance or the sum of transfers to it), `deadline`, `monthly` = (target - current) / months left.
- `kind` `debt`: `current` the amount still owed, `rate` the yearly interest, `monthly` the payment needed to clear it by `deadline` (with interest: payment = r·P / (1 - (1 + r)^-n), r monthly rate, n months).
- Several debts: pay the minimum on all, the extra on the highest rate first (avalanche); the smallest first (snowball) only when the person prefers it.

## The check (#goal-check)
- On track when what went in over the last 3 months ≥ `monthly`. Off track: the options with numbers (more each month, a later date, a smaller target, a cut in a named category), the person chooses.
- Can I afford it? The amount against this month's budget left, the buffer and the goals' monthly amounts; say what it delays.
- Reached: `status` `achieved`, one line of congratulation.
