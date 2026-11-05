#!/usr/bin/env python3
"""Money totals for the finance notebook (skills report, budget, goals), worked out
by code, not guessed. Standard library only; reads office's database read-only.

  python3 .claude/skills/report/totals.py month   [--month YYYY-MM]
  python3 .claude/skills/report/totals.py months  [--last 3]
  python3 .claude/skills/report/totals.py budget  [--month YYYY-MM]
  python3 .claude/skills/report/totals.py goals
  python3 .claude/skills/report/totals.py search  [--category C] [--account A] [--from D] [--to D] [--text T]

The project and the database are found on their own; set them when that fails:
  OFFICE_DB    path of office.db
  FIN_PROJECT  the project's id (rep_...)
"""
import argparse, json, os, re, sqlite3, sys
from collections import Counter, defaultdict
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]  # .claude/skills/report/totals.py -> the project


def find_db():
    if os.environ.get("OFFICE_DB"):
        return os.environ["OFFICE_DB"]
    for p in [ROOT, *ROOT.parents]:  # a chat's worktree lives in <office>/.office/worktrees/...
        if (p / ".office" / "office.db").is_file():
            return str(p / ".office" / "office.db")
    for p in (Path.home() / ".agent-office" / "office.db", Path.home() / ".agent-office" / ".office" / "office.db"):
        if p.is_file():
            return str(p)
    sys.exit("office.db not found: set OFFICE_DB=<path of office.db>")


def find_project(con):
    if os.environ.get("FIN_PROJECT"):
        return os.environ["FIN_PROJECT"]
    for rid, path in con.execute("select id, path from repos where path != ''"):
        try:
            if Path(path).resolve() == ROOT:
                return rid
        except OSError:
            pass
    m = re.search(r"/worktrees/(rep_[0-9a-z]+)", str(ROOT))
    if m:
        return m.group(1)
    sys.exit("project not found: set FIN_PROJECT=<the project's id, rep_...>")


CON = sqlite3.connect(f"file:{find_db()}?mode=ro", uri=True)
PROJECT = find_project(CON)


def load(key):
    rows = CON.execute("select r.id, r.data from records r join collections c on c.id=r.collection_id "
                       "where r.project_id=? and c.key=?", (PROJECT, key)).fetchall()
    out = []
    for rid, data in rows:
        o = json.loads(data)
        o["_id"] = rid
        out.append(o)
    return out


def num(x):
    try:
        return float(x or 0)
    except (TypeError, ValueError):
        return 0.0


def money(x):
    return f"{x:,.0f}".replace(",", ".")


def month_of(t):
    return t.get("month") or str(t.get("date", ""))[:7]


def last_month():
    t = date.today().replace(day=1)
    y, m = (t.year, t.month - 1) if t.month > 1 else (t.year - 1, 12)
    return f"{y}-{m:02d}"


def prev_months(month, n):
    y, m = int(month[:4]), int(month[5:7])
    out = []
    for _ in range(n):
        out.append(f"{y}-{m:02d}")
        y, m = (y, m - 1) if m > 1 else (y - 1, 12)
    return out[::-1]


def tx():
    return load("transactions")


def cmd_month(a):
    month = a.month or date.today().strftime("%Y-%m")
    rows = [t for t in tx() if month_of(t) == month]
    inc = sum(num(t["amount"]) for t in rows if t.get("type") == "income")
    exp = sum(num(t["amount"]) for t in rows if t.get("type") == "expense")
    print(f"month {month}  project {PROJECT}  transactions {len(rows)}")
    print(f"income {money(inc)}  expenses {money(exp)}  saved {money(inc - exp)}  "
          f"savings rate {((inc - exp) / inc * 100) if inc else 0:.1f}%")
    by_cat = Counter()
    by_acc = Counter()
    for t in rows:
        if t.get("type") == "expense":
            by_cat[t.get("category") or "?"] += num(t["amount"])
            by_acc[t.get("account") or "?"] += num(t["amount"])
    print("expenses by category:")
    for c, v in by_cat.most_common():
        print(f"  {c:<24} {money(v):>14}  {v / exp * 100 if exp else 0:5.1f}%")
    print("expenses by account: " + "  ".join(f"{k}={money(v)}" for k, v in by_acc.most_common()))
    big = sorted((t for t in rows if t.get("type") == "expense"), key=lambda t: -num(t["amount"]))[:10]
    print("biggest expenses:")
    for t in big:
        print(f"  {t.get('date')}  {money(num(t['amount'])):>12}  {t.get('category', '')}  {t.get('merchant', '')}  {t.get('note', '')}")
    todo = sum(1 for t in rows if t.get("status") == "to-check")
    if todo:
        print(f"to-check: {todo}")


def cmd_months(a):
    end = a.month or last_month()
    months = prev_months(end, a.last)
    grid = defaultdict(Counter)
    inc = Counter()
    for t in tx():
        m = month_of(t)
        if m in months:
            if t.get("type") == "expense":
                grid[t.get("category") or "?"][m] += num(t["amount"])
            elif t.get("type") == "income":
                inc[m] += num(t["amount"])
    print("category".ljust(24) + "".join(m.rjust(14) for m in months) + "average".rjust(14))
    for c in sorted(grid, key=lambda c: -sum(grid[c].values())):
        vals = [grid[c][m] for m in months]
        print(c[:24].ljust(24) + "".join(money(v).rjust(14) for v in vals) + money(sum(vals) / len(months)).rjust(14))
    tot = [sum(grid[c][m] for c in grid) for m in months]
    print("TOTAL EXPENSES".ljust(24) + "".join(money(v).rjust(14) for v in tot) + money(sum(tot) / len(months)).rjust(14))
    print("INCOME".ljust(24) + "".join(money(inc[m]).rjust(14) for m in months) + money(sum(inc.values()) / len(months)).rjust(14))


def cmd_budget(a):
    month = a.month or date.today().strftime("%Y-%m")
    spent = Counter()
    for t in tx():
        if month_of(t) == month and t.get("type") == "expense":
            spent[t.get("category") or "?"] += num(t["amount"])
    buds = [b for b in load("budgets") if b.get("month") == month]
    if not buds:
        print(f"no budget for {month}; spending so far: " + "  ".join(f"{k}={money(v)}" for k, v in spent.most_common()))
        return
    print(f"budget {month}")
    print("category".ljust(24) + "limit".rjust(14) + "spent".rjust(14) + "%".rjust(7) + "  status")
    for b in sorted(buds, key=lambda b: -spent[b.get("category")] / (num(b.get("limit")) or 1)):
        lim, s = num(b.get("limit")), spent[b.get("category")]
        pct = s / lim * 100 if lim else 0
        st = "over" if pct > 100 else "warning" if pct >= 80 else "ok"
        print(str(b.get("category"))[:24].ljust(24) + money(lim).rjust(14) + money(s).rjust(14) + f"{pct:6.0f}%  {st}")
    other = {k: v for k, v in spent.items() if k not in {b.get("category") for b in buds}}
    if other:
        print("spent outside the budget: " + "  ".join(f"{k}={money(v)}" for k, v in other.items()))


def cmd_goals(a):
    goals = [g for g in load("goals") if g.get("status", "active") == "active"]
    months = prev_months(last_month(), 3)
    flows = Counter()
    for t in tx():
        if month_of(t) in months and t.get("type") == "transfer":
            flows[t.get("to_account") or "?"] += num(t["amount"])
    today = date.today()
    for g in goals:
        target, cur = num(g.get("target")), num(g.get("current"))
        left = (date.fromisoformat(g["deadline"]).year - today.year) * 12 + date.fromisoformat(g["deadline"]).month - today.month if g.get("deadline") else None
        need = ((target - cur) if g.get("kind") != "debt" else cur) / left if left and left > 0 else None
        per = flows[g.get("account")] / 3 if g.get("account") else None
        print(f"{g.get('title')} [{g.get('kind', 'saving')}]  current {money(cur)}  target {money(target)}  "
              f"months left {left}  monthly needed {money(need) if need is not None else '-'}  "
              f"last 3 months avg into {g.get('account') or '-'}: {money(per) if per is not None else '-'}")


def cmd_search(a):
    rows = []
    for t in tx():
        if a.category and (t.get("category") or "").lower() != a.category.lower():
            continue
        if a.account and (t.get("account") or "").lower() != a.account.lower():
            continue
        if a.from_ and str(t.get("date", "")) < a.from_:
            continue
        if a.to and str(t.get("date", "")) > a.to:
            continue
        if a.text and a.text.lower() not in json.dumps(t, ensure_ascii=False).lower():
            continue
        rows.append(t)
    rows.sort(key=lambda t: str(t.get("date", "")))
    for t in rows:
        print(f"{t.get('date')}  {t.get('type', ''):<8} {money(num(t.get('amount'))):>12}  {t.get('category', '')}  {t.get('account', '')}  {t.get('merchant', '')}")
    tot = Counter()
    for t in rows:
        tot[t.get("type")] += num(t.get("amount"))
    print(f"{len(rows)} transactions  " + "  ".join(f"{k}={money(v)}" for k, v in tot.items()))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("month"); s.add_argument("--month"); s.set_defaults(fn=cmd_month)
    s = sub.add_parser("months"); s.add_argument("--month"); s.add_argument("--last", type=int, default=3); s.set_defaults(fn=cmd_months)
    s = sub.add_parser("budget"); s.add_argument("--month"); s.set_defaults(fn=cmd_budget)
    s = sub.add_parser("goals"); s.set_defaults(fn=cmd_goals)
    s = sub.add_parser("search")
    for x in ("--category", "--account", "--to", "--text"):
        s.add_argument(x)
    s.add_argument("--from", dest="from_")
    s.set_defaults(fn=cmd_search)
    a = ap.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
