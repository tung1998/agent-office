---
name: budget
description: Set and watch a monthly budget by category (table budgets) from the last months' spending, fixed bills, income and saving goals; warn at 80% and over 100%. Use when planning next month, after recording spending, or when the person asks how much is left.
---

# Budget

## Making one (#budget-plan)
1. The base: the last 3 months by category (`python3 .claude/skills/report/totals.py months --last 3`), the income expected, fixed bills (rent, utilities, loans, insurance, subscriptions) and yearly costs coming (school fees, Tết, insurance renewals: one twelfth set aside each month).
2. Goals first: each active `goals` record's `monthly` is a line of its own, funded before the flexible categories.
3. Limits near the 3-month average; a cut the person wants is said as a number ("Ăn ngoài 3tr → 2,4tr, -20%"). A guide when they ask for one: 50% needs, 30% wants, 20% savings, adjusted to their income.
4. A buffer line (about 5%) for what nobody planned. The total of limits plus savings ≤ income; over it: say so and offer cuts.
5. Written only after the person's yes: one `budgets` record per category (`month`, `category`, `limit`, `spent` 0, `status` `ok`).

## Watching it
- After each recording: the category's `spent` this month (`totals.py budget`); `status` `warning` at 80%, `over` above 100%. Tell the person once per threshold, not after every coffee.
- How much is left: limit - spent per category, and per day left in the month.
- A category over two months running: a `lessons` record and a question at the next plan (raise the limit, or change the habit).
