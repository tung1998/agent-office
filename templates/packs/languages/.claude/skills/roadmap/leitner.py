#!/usr/bin/env python3
"""Leitner numbers for the roadmap (skills `roadmap`, `review`, `notes`), worked out
by code, not guessed. Standard library only; reads office's database read-only.

  python3 .claude/skills/roadmap/leitner.py stats    [--today D]
  python3 .claude/skills/roadmap/leitner.py simulate [--from D] [--to D] [--cap N] [--new M] [--new-from D] [--new-until D]
                      [--off D,...] [--acc 0.55,0.70,0.85,0.85,0.90] [--threshold N]
  python3 .claude/skills/roadmap/leitner.py check    [--from D] [--days 14] [--cap N]

Dates are YYYY-MM-DD; default today. The project and the database are found on
their own; set them when that fails:
  OFFICE_DB        path of office.db
  LEITNER_PROJECT  the project's id (rep_...)
Box intervals are the skill `review`'s: 1/3/7/14/30 days.
"""
import argparse, json, os, re, sqlite3, sys
from collections import Counter
from datetime import date, timedelta
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]  # .claude/skills/roadmap/leitner.py → the project
INTERVAL = {1: 1, 2: 3, 3: 7, 4: 14, 5: 30}
DEFAULT_ACC = [0.55, 0.70, 0.85, 0.85, 0.90]  # right-rate per box: an assumption, adjust with --acc
ITEMS_PER_MINUTE = 1.5  # review items a learner gets through per minute, for the session size


def d(s):
    return date.fromisoformat(s)


def find_db():
    if os.environ.get("OFFICE_DB"):
        return os.environ["OFFICE_DB"]
    # a chat's worktree lives in <office>/.office/worktrees/...: walk up
    for p in [ROOT, *ROOT.parents]:
        if (p / ".office" / "office.db").is_file():
            return str(p / ".office" / "office.db")
    for p in (Path.home() / ".agent-office" / "office.db", Path.home() / ".agent-office" / ".office" / "office.db"):
        if p.is_file():
            return str(p)
    sys.exit("office.db not found: set OFFICE_DB=<path of office.db>")


def connect():
    return sqlite3.connect(f"file:{find_db()}?mode=ro", uri=True)


def find_project(con):
    if os.environ.get("LEITNER_PROJECT"):
        return os.environ["LEITNER_PROJECT"]
    for rid, path in con.execute("select id, path from repos where path != ''"):
        try:
            if Path(path).resolve() == ROOT:
                return rid
        except OSError:
            pass
    m = re.search(r"/worktrees/(rep_[0-9a-z]+)", str(ROOT))  # a chat's worktree names its project
    if m:
        return m.group(1)
    sys.exit("project not found: set LEITNER_PROJECT=<the project's id, rep_...>")


CON = None
PROJECT = None


def load(key):
    global CON, PROJECT
    if CON is None:
        CON = connect()
        PROJECT = find_project(CON)
    rows = CON.execute(
        "select r.id, r.data from records r join collections c on c.id=r.collection_id "
        "where r.project_id=? and c.key=?", (PROJECT, key)).fetchall()
    out = []
    for rid, data in rows:
        o = json.loads(data)
        o["_id"] = rid
        out.append(o)
    return out


def profile():
    """Exam date, minutes a day and study days a week from docs/profile.md."""
    p = ROOT / "docs" / "profile.md"
    txt = p.read_text(encoding="utf-8") if p.is_file() else ""
    out = {"exam": None, "minutes": None, "days": 6}
    for line in txt.splitlines():
        low = line.lower()
        if "ngày thi" in low or "exam" in low:
            m = re.search(r"(\d{4})-(\d{1,2})-(\d{1,2})", line)
            n = re.search(r"(\d{1,2})[./](\d{1,2})[./](\d{4})", line)
            if m:
                out["exam"] = date(int(m[1]), int(m[2]), int(m[3]))
            elif n:
                out["exam"] = date(int(n[3]), int(n[2]), int(n[1]))
        elif "mỗi ngày" in low or "per day" in low:
            m = re.search(r"(\d+)\s*(phút|min)", low)
            h = re.search(r"(\d+(?:[.,]\d+)?)\s*(giờ|h\b|hour)", low)
            if m:
                out["minutes"] = int(m[1])
            elif h:
                out["minutes"] = int(float(h[1].replace(",", ".")) * 60)
        elif "ngày học mỗi tuần" in low or "days a week" in low:
            m = re.search(r"(\d)", line.split(":", 1)[-1])
            if m:
                out["days"] = max(1, min(7, int(m[1])))
    return out


def study_days(n):
    return set(range(n))  # Monday first; the rest of the week is off


def box(c):
    try:
        return max(1, min(5, int(c.get("box") or 1)))
    except (TypeError, ValueError):
        return 1


def due_of(c, today):
    try:
        return d(c["next_review"]) if c.get("next_review") else today
    except ValueError:
        return today


def tags(c):
    t = c.get("tags") or []
    return t if isinstance(t, list) else [x.strip() for x in str(t).split(",") if x.strip()]


def items():
    """Vocabulary, grammar and open mistakes: everything the review boxes hold."""
    out = []
    for key in ("vocabulary", "grammar"):
        out += [c for c in load(key) if c.get("status") != "mastered"]
    out += [c for c in load("mistakes") if c.get("status") != "fixed"]
    return out


def session_cap(a, prof):
    if a.cap:
        return a.cap
    if prof["minutes"]:
        return max(5, round(prof["minutes"] * 0.5 * ITEMS_PER_MINUTE))  # about half the day's time is review
    return 25


# ---------------------------------------------------------------- stats
def cmd_stats(a):
    today = d(a.today) if a.today else date.today()
    voc, gram, mis = load("vocabulary"), load("grammar"), load("mistakes")
    print(f"today {today}  project {PROJECT}")
    for name, cards in (("vocabulary", voc), ("grammar", gram)):
        print(f"{name}: {len(cards)}  " + "  ".join(f"box{b}={sum(1 for c in cards if box(c) == b)}" for b in range(1, 6))
              + "  | " + "  ".join(f"{k}={v}" for k, v in sorted(Counter(c.get('status', '?') for c in cards).items())))
    live = items()
    due = [c for c in live if due_of(c, today) <= today]
    print(f"due today or overdue: {len(due)}  (overdue before today: {sum(1 for c in due if due_of(c, today) < today)})")
    print("  weak among them: hard=%d priority=%d error:*=%d mistakes=%d" % (
        sum(1 for c in due if c.get("status") == "hard"),
        sum(1 for c in due if "priority" in tags(c)),
        sum(1 for c in due if any(t.startswith("error:") for t in tags(c))),
        sum(1 for c in due if "wrong" in c and "fixed" in c)))
    paused = [c for c in live if "paused" in tags(c)]
    if paused:
        print(f"paused (held new items): {len(paused)}, next due {min(due_of(c, today) for c in paused)}")
    ahead = Counter(due_of(c, today) for c in live if due_of(c, today) > today)
    print("due next days: " + "  ".join(f"{k.isoformat()[5:]}={v}" for k, v in sorted(ahead.items())[:10]))
    errs = Counter(t for c in voc + gram for t in tags(c) if t.startswith("error:"))
    if errs:
        print("error tags: " + "  ".join(f"{k}={v}" for k, v in errs.most_common(6)))
    print(f"mistakes open: {sum(1 for m in mis if m.get('status') != 'fixed')}, by kind: "
          + str(dict(Counter(m.get('kind') for m in mis if m.get('status') != 'fixed').most_common(5))))
    sched = load("schedule")
    week = [t for t in sched if t.get("date") and today <= d(t["date"]) < today + timedelta(days=7)]
    print("schedule next 7 days: " + str(dict(Counter(t.get("status") for t in week))))
    prof = profile()
    print(f"profile: exam {prof['exam']}  minutes/day {prof['minutes']}  days/week {prof['days']}  review cap/day {session_cap(a, prof)}")


# ---------------------------------------------------------------- simulate
def simulate(start, end, cap, new, new_from, new_until, off, acc, threshold, days, cards, out=None):
    mass = Counter()
    for c in cards:
        mass[(box(c), max(due_of(c, start), start))] += 1
    rows, day = [], start
    while day <= end:
        due = done = added = 0
        if day.weekday() in days and day not in off:
            due = sum(m for (b, dd), m in mass.items() if dd <= day)
            left = min(cap, due)
            done = left
            for k in sorted((k for k in mass if k[1] <= day and mass[k] > 0), key=lambda k: (k[0], k[1])):
                if left <= 0:
                    break
                take = min(mass[k], left)
                mass[k] -= take
                left -= take
                up = take * acc[k[0] - 1]
                nb = min(5, k[0] + 1)
                mass[(nb, day + timedelta(days=INTERVAL[nb]))] += up
                mass[(1, day + timedelta(days=1))] += take - up
            if new and new_from and new_from <= day <= new_until and due < threshold:
                added = new
                mass[(1, day + timedelta(days=1))] += new
        solid = sum(m for (b, _), m in mass.items() if b >= 3)
        rows.append((day, due, done, added, solid))
        day += timedelta(days=1)
    return rows


def cmd_simulate(a):
    prof = profile()
    start = d(a.from_) if a.from_ else date.today()
    end = d(a.to) if a.to else (prof["exam"] or start + timedelta(days=27))
    acc = [float(x) for x in a.acc.split(",")] if a.acc else DEFAULT_ACC
    cap = session_cap(a, prof)
    threshold = a.threshold if a.threshold else cap
    rows = simulate(start, end, cap, a.new, d(a.new_from) if a.new_from else (start if a.new else None),
                    d(a.new_until) if a.new_until else end, {d(x) for x in a.off.split(",")} if a.off else set(),
                    acc, threshold, study_days(prof["days"]), items())
    print(f"simulate {start}..{end}  cap {cap}/day  new {a.new}/day while due < {threshold}  acc {acc}")
    print("date        day  due  reviewed  new  | box>=3")
    for day, due, done, added, solid in rows:
        print(f"{day}  {day.strftime('%a')}  {due:4.0f}  {done:8.0f}  {added:3.0f}  | {solid:6.0f}")
    over = [r for r in rows if r[1] > cap]
    print(f"days over the cap: {len(over)}" + (f", last {over[-1][0]}" if over else ""))


# ---------------------------------------------------------------- check
def cmd_check(a):
    """Is the next stretch doable? The backlog, and when new items can resume."""
    prof = profile()
    start = d(a.from_) if a.from_ else date.today()
    end = start + timedelta(days=a.days - 1)
    cap = session_cap(a, prof)
    rows = simulate(start, end, cap, 0, None, end, set(), DEFAULT_ACC, cap, study_days(prof["days"]), items())
    over = [r for r in rows if r[1] > cap]
    print(f"check {start}..{end}  review cap {cap}/day (profile {prof['minutes']} min/day)")
    print(f"due today: {rows[0][1]:.0f}" if rows else "no days")
    if not over:
        print("OK: the reviews fit; new items can go on as planned")
    else:
        resume = next((r[0] for r in rows if r[0] > over[-1][0] and r[1] <= cap), None)
        print(f"BACKLOG: {len(over)} day(s) over the cap, worst {max(r[1] for r in over):.0f} due on "
              f"{max(over, key=lambda r: r[1])[0]}")
        print(f"pause new items; they can resume on {resume or 'after ' + end.isoformat()} "
              "(record new words with that next_review and the tag paused, skill notes)")
    sched = [t for t in load("schedule") if t.get("date") and start <= d(t["date"]) <= end]
    if sched and prof["minutes"]:
        per = Counter()
        for t in sched:
            try:
                per[t["date"]] += int(t.get("minutes") or 0)
            except (TypeError, ValueError):
                pass
        heavy = {k: v for k, v in per.items() if v > prof["minutes"] * 1.2}
        print("schedule over the day's time: " + (", ".join(f"{k} {v} min" for k, v in sorted(heavy.items())) or "none"))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("stats")
    s.add_argument("--today")
    s.add_argument("--cap", type=int)
    s.set_defaults(fn=cmd_stats)
    s = sub.add_parser("simulate")
    s.add_argument("--from", dest="from_")
    s.add_argument("--to")
    s.add_argument("--cap", type=int)
    s.add_argument("--new", type=float, default=0)
    s.add_argument("--new-from")
    s.add_argument("--new-until")
    s.add_argument("--off")
    s.add_argument("--acc")
    s.add_argument("--threshold", type=int)
    s.set_defaults(fn=cmd_simulate)
    s = sub.add_parser("check")
    s.add_argument("--from", dest="from_")
    s.add_argument("--days", type=int, default=14)
    s.add_argument("--cap", type=int)
    s.set_defaults(fn=cmd_check)
    a = ap.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
