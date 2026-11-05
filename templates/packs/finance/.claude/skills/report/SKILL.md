---
name: report
description: Monthly money report and every total (by month, category, account, budget, goals) worked out by the script totals.py from the office data, not by hand. Use for the monthly report, any "how much did I spend on X", and before a budget or goal check.
---

# Reports and totals

## The script
`python3 .claude/skills/report/totals.py` (standard library Python, reads office's database read-only; it finds the project and the database on its own, else set `OFFICE_DB` and `FIN_PROJECT`):
- `totals.py month [--month YYYY-MM]`: income, expenses, savings rate, by category and account, the 10 biggest expenses.
- `totals.py months --last 3`: categories by month side by side, with the average.
- `totals.py budget [--month YYYY-MM]`: each budget's limit, spent, % and status.
- `totals.py goals`: each goal with what went in over the last 3 months.
- `totals.py search --category Ăn uống --from D --to D`: the matching transactions and their total.
Quote its numbers. Cannot run (no Python, no database): sum with `data_query` and say so.

## The monthly report (`reports/<YYYY-MM>.md`)
```markdown
# Báo cáo tháng <MM/YYYY>
| Thu | Chi | Để dành | Tỷ lệ để dành |
## Theo danh mục (so ngân sách, so trung bình 3 tháng)
## Điểm đáng chú ý
- changes over 20%, transactions over 10% of the month, new subscriptions, to-check left
## Mục tiêu và nợ
## Gợi ý cho tháng sau (lựa chọn, không phải lời khuyên)
```
Short: the person reads it in two minutes. Every claim traced to transactions.
