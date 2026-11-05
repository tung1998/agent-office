---
name: roadmap
description: Plan a learner's way to a goal by a date (an exam, a course end, a project): check it is feasible with the numbers, plan backwards in phases and weeks, schedule the next two weeks in table schedule, place checkpoints and mock tests, adapt every week. Use when the learner states a goal or a date, asks for a plan, misses days, or the weekly check runs.
---

# Roadmap

## 1. Start
The goal and date, the subject's syllabus or the exam's topics (from the learner's course or the official site, kept in `resources/exam-formats.md` with the source), what is known already (`concepts` by box, a short diagnostic quiz when there is nothing yet), the time a day and days a week (`docs/profile.md`).

## 2. Feasible?
- The gap: topics to learn × the time each takes at the learner's pace (a first lesson and practice, then about 5 reviews in the first month).
- The time: days to the date × minutes a day, less a rest day a week and a 10-15% buffer.
- The review load grows with what was learned: forecast it with `leitner.py simulate`; new topics taper off before the date.
- Not feasible: say so with the numbers and offer choices (more time a day, a later date, a narrower goal). The learner chooses.

## 3. Plan backwards
Phases from the date back: the last 1-2 weeks mock tests and review only; before that the core topics in an order that respects prerequisites; a ramp-up when the base is shaky. Each phase ends with a gate: about 80% of its concepts at box 3 or more and its checkpoint passed. Each week: a target that can be checked, a theme, a checkpoint every 2 weeks (#practice as a quiz), a full #mock-test 2-3 weeks before the date.

## 4. Written
`roadmap/<YYYY-MM-DD>-<goal>.md` (goal, start, feasibility numbers, phases, weeks table, `## Progress`, `## Changes`), linked in `docs/profile.md`. The next 2 weeks as `schedule` records (`date`, `week`, `title`, `kind`, `target`, `minutes`, `status` `todo`, `plan`).

## 5. Adapt every week
Done vs planned, review accuracy, scores, `weak_tags`, the journal lines; `leitner.py check` for the backlog. On track: schedule the next 2 weeks. Behind or a backlog over a day's session: fewer new topics, never less review; new concepts held until the date `check` gives (record them with that `next_review` and the tag `paused`). A gate not passed: one more week on what is under box 3. Write the week's row and a line in `## Changes`; tell the learner in two or three lines.

## 6. Numbers from the script
`.claude/skills/roadmap/leitner.py` (standard library Python, reads office's database read-only; finds the project and the database on its own, else set `OFFICE_DB` and `LEITNER_PROJECT`):
- `python3 .claude/skills/roadmap/leitner.py stats`: concepts by box and status, due, weak, held, what comes due next.
- `python3 .claude/skills/roadmap/leitner.py check`: the next 14 days against the day's review cap: OK, or BACKLOG with the date new concepts can resume.
- `python3 .claude/skills/roadmap/leitner.py simulate --new 3 --to <date>`: the boxes day by day with N new concepts a day.
Quote its numbers; the right-rates per box are an assumption. Cannot run: estimate and say so.

## Mock tests
In the real exam's structure, timing and marking, checked on the web with the source; graded like the exam (#grade), then the weak topics go into the next weeks.
