---
name: ledger
description: Record a person's income, spending and transfers in the money notebook (table transactions) from chat messages, receipt photos or bank and e-wallet statement exports, with the right date, category and account and no duplicates. Use whenever the person mentions money they spent or received, or sends a receipt or a statement.
---

# Recording transactions

## One record per transaction (`transactions`, data_upsert)
| Field | What to write |
|---|---|
| `date` | The day it happened (`YYYY-MM-DD`); "hôm qua", "thứ 2" worked out from today |
| `amount` | Positive number in the currency's units: "45k" = 45000, "1tr2" = 1200000, "2,5 triệu" = 2500000 |
| `type` | `expense`, `income`, or `transfer` (between two of the person's own accounts: `account` from, `to_account` to) |
| `category` / `subcategory` | From `docs/categories.md`; a new one only when nothing fits, said in the reply |
| `account` | The account's name in `accounts` (cash when not said and it looks like cash) |
| `merchant` | Shop, service or payer |
| `currency` | Only when not the profile's; the rate and its date in `note` |
| `source` | `message`, `receipt`, `statement`, `manual`; `receipt` the image path when kept |
| `month` | `YYYY-MM` of `date` |
| `status` | `recorded`, or `to-check` when the amount, date or category is unsure (say why in `note`) |

## Sources
- **Messages**: several in one line ("ăn trưa 50k, grab 32k") are separate records. A refund is an `income` in the same category, noted as refund.
- **Receipts**: one record per receipt (its total), not per item, unless the person wants items split across categories. VAT and service charges are in the total. Unreadable: `to-check`.
- **Statements** (CSV, Excel, PDF exports): one record per row of money in or out; skip opening and closing balances, subtotal lines and pending holds. Card payments to the person's own credit card are a `transfer`. Fees and interest are expenses (category fees).
- Never record or repeat a card number, full account number, password, PIN or OTP: keep only the account's name. Statement rows often hold them in the description: drop them.

## No duplicates
`data_query` `transactions` with the same `date` and `amount` (and `merchant` when given) before writing. A statement row matching a message already recorded: keep one (the statement's), update `source`.

## Categories
`docs/categories.md` is the person's list; keep their words. Default groups when it is empty: Ăn uống, Đi lại, Nhà cửa và hóa đơn, Mua sắm, Sức khỏe, Giáo dục, Giải trí, Gia đình và quà, Phí và lãi, Thu nhập, Tiết kiệm và đầu tư, Khác.

## Tell the person
What was recorded (one table: date, amount, category, account), what is to-check and why, the category's month total vs budget when it reached 80%.
