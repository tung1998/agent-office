---
name: roadmap
description: Build and adapt a language learner's roadmap to a goal by a date (e.g. JLPT N3 in 2 months, IELTS 6.5 by June): check it is feasible, plan backwards in phases and weeks, schedule the daily tasks in table schedule, place checkpoint tests, then re-plan each week from what was done and the scores. Use when the learner states a goal or a deadline, asks for a plan or schedule, misses days, or the weekly check runs.
---

# Roadmap

A roadmap turns a goal and a date into weeks and days the learner can follow. It lives in a file, its days in the table `schedule`, and it changes when the learner's life or results do.

## 1. What the plan stands on
Read `docs/profile.md`: language, current level, goal, exam and its date, time per day, days per week, skills to favour, how they like to learn. Missing the goal, the date or the time per day: ask those three, briefly, before planning; the rest can wait.

Measure where they stand, not where they say: count `vocabulary` and `grammar` by `level` and `status`, the last `exercises` scores and `weak_tags`. Nothing recorded yet: plan a short placement check (#mock-test, one shortened section of the level below the goal) as the first task, and skip in the plan what it shows they already know.

The kind of plan (profile "Kiểu lộ trình", ask when the goal does not make it clear): `exam` (an exam: its sections and format, practice tests), `balanced` (all four skills), `conversation` (speaking and listening, everyday topics), `reading` (reading, vocabulary). It sets the share of time per skill.

| Kind | Words, grammar, characters (with review) | Reading | Listening | Speaking | Writing | Practice tests |
|---|---|---|---|---|---|---|
| `exam` | 40% | 15% | 15% | by the exam | by the exam | 15-30%, more near the date |
| `balanced` | 35% | 15% | 15% | 20% | 15% | checkpoints only |
| `conversation` | 30% | 5% | 30% | 30% | 5% | checkpoints only |
| `reading` | 40% | 35% | 10% | 5% | 10% | checkpoints only |

An exam with no speaking or writing part (JLPT, TOEIC Listening & Reading) gives that time to its parts, but keep 10% for speaking and writing unless the learner says no. Skills practice follows the skill `skills-practice`.

## 2. Is it feasible
- The gap: what the target level needs (vocabulary, characters, grammar patterns, listening/reading hours, exam sections) minus what is known. The level's requirements: look them up from official or trustworthy sources (the Test writer has the web) and keep them with their source in `resources/level-standards.md`; read that file first next time. Say figures are estimates when the exam publishes none (JLPT publishes no word list).
- The time: days to the date × minutes per day, less one rest day a week and a 10-15% buffer for missed days and the final review.
- The load per day it gives (new words, patterns, review, practice). Compare with what a learner can hold: new vocabulary much above 30-40 items a day, or under 20 minutes of review a day, does not stick.
- The review it creates: every new item comes back about 5 times in its first month (boxes 1-4, skill `review`), so the review load of a week is roughly the new items of the 3-4 weeks before it. Forecast it week by week (about 20-30 seconds per review item) and lower the new items when review would take more than half the day's minutes. This is why new material tapers off before the exam.
- Not feasible: say so plainly with the numbers, then offer two or three ways out (more time per day, a later date, a narrower goal such as passing the reading part, a lower level first). The learner chooses; never quietly plan for a goal that cannot be met.

## 3. Plan backwards
1. **Phases**: from the date back. The last 1-2 weeks: full practice tests and review only, no new material. Before that: the core (new material by topic and weak areas), then a ramp-up at the start if the base is shaky.
2. **Weeks**: each week has a target that can be checked (e.g. "N3 words: 200 new, kanji 60, grammar 15 patterns; listening 2 h"), a theme, the forecast review minutes, and its checkpoint.
3. **Checkpoints**: a short test in the exam's format every 2 weeks (#mock-test), a full mock test 2-3 weeks before the date. Each has a pass mark; under it the plan is adapted (section 5).
4. **Mastery gate**: each phase (and each topic block inside the core) ends with a gate, written in the plan: about 80% of its words, characters and patterns at box 3 or higher (skill `review`), and its checkpoint at or above the pass mark. Material of the next phase is not scheduled until the gate is passed; the weekly check (section 5) decides.
5. **Days**: by the days per week and minutes per day. Every day keeps review (skill `review`) first, then new material, then one skill's practice (rotating, so each skill comes every week by its share); one lighter day a week. Match the learner's way of learning (short sessions, more examples, more listening) from the profile and memory `learner`.

## 4. Where it is written
- The plan: `roadmap/<YYYY-MM-DD>-<goal>.md` (date it was made), with: goal and date, kind of plan, starting point (counts and scores), feasibility (the numbers, the choices made), phases, a table of weeks (target, theme, review minutes, checkpoint), a `## Progress` table (per week: planned vs done, review accuracy, checkpoint score, gate passed or not, and a 0-5 level per skill: vocabulary, grammar, characters, listening, reading, speaking, writing), and a `## Changes` log (date, what changed, why).
- The current plan: link it in `docs/profile.md` at "Lộ trình hiện tại". One current plan at a time; a new goal makes a new file, the old one is kept with its final status in `## Changes`.
- The days: one `schedule` record per task, only for the next 2 weeks (the rest stays in the week table and is scheduled at the weekly check). Fields: `date`, `week`, `title`, `kind`, `target` (concrete: "20 new words, topic work, N3"), `minutes`, `status` = `todo`, `plan` = the plan file, `tags` (`exam:<exam>`, `level:<level>`, topic).
- Tell the learner the plan in short: the weeks in one table, this week day by day, the first checkpoint. Not the whole file.

## 5. Adapt it
At the weekly check (the schedule, or when asked), and at once when the learner says their time or date changed:
1. This week: `schedule` done (`done`), partly done (`partial`), skipped (`skipped`); the review backlog (items with `next_review` before today); review accuracy (right / reviewed, from `correct` and `wrong` this week); checkpoint scores and `weak_tags`; the `## Journal` lines of the week's `lessons/` files (what felt hard, confidence: a topic rated 1-2 twice is a weak area even with good scores). Write the week's row in `## Progress`.
2. Decide by the numbers:
   - On track (≥ 85% done, checkpoint passed): keep it, schedule the next 2 weeks.
   - A phase ending: check its gate (section 3). Passed: open the next phase. Not passed: keep the phase one more week on what is under box 3 and the checkpoint's `weak_tags` (review and focused practice, few new items), then check again; failed twice: say so and re-plan with section 2, never move on silently.
   - Behind (< 70% done, or the review backlog over a day's session): lower the new material, never the review; spread what was missed over the next weeks within the buffer; the buffer used up: say so and offer a choice (more time, later date, narrower goal).
   - Ahead (everything done, high scores): offer more, or an earlier mock test.
   - Review accuracy keeps learning in its best band, about 70-85%: under 60%, too much new at once (fewer new items, more grammar in context); above 90% for two weeks, room for more.
   - A weak area: the week's 3 most frequent mistake kinds (`mistakes` by `kind` and `count`, tags `error:*`) and `weak_tags`; one seen two weeks running gets more time next week and a focused exercise.
   - The learner's life changed (less time, a trip, a new date): re-plan from today with section 2.
3. Write it: update the week table, the next 2 weeks of `schedule` (past tasks keep their status; moved tasks get `rescheduled` and a new record), and a line in `## Changes`. Tell the learner what changed and why in two or three lines.

## Every day
- Little and every day beats a long session once a week: keep a short day (review only, about 10 minutes, skill `review`) for busy days, instead of skipping. Days skipped in a row: offer the short day first, then re-plan if it lasts.
- The day's tasks: `data_query` `schedule` with `date` = today, `status` = `todo`.
- When a task is done (words recorded, a review graded, a test done): set its `status` and a short `result` ("18/20 words", "review 22/25", "test 31/40").
