---
name: review
description: Spaced repetition for a learner (Leitner boxes) over table concepts and the mistake log: what is due, grading rules, box moves, mistakes, hard items. Use for the daily review, when grading, and when picking what to practise.
---

# Review

## Boxes
| Box | Again after |
|---|---|
| 1 | 1 day |
| 2 | 3 days |
| 3 | 7 days |
| 4 | 14 days |
| 5 | 30 days |
- Right: up one box (at most 5), `next_review` = today + its interval; right twice in box 5: `status` `mastered`.
- Wrong: box 1, `next_review` tomorrow, `status` `learning`, tag `error:<kind>` (`error:concept` misunderstood, `error:method` wrong method, `error:calculation` arithmetic or algebra slip, `error:reading` misread the question, `error:syntax` code syntax, `error:logic` code logic, `error:edge-case` missed a case).
- Right method with a slip: keep the box, again in 1 day.
- Always `last_review`, `correct` or `wrong`. Wrong 3 times or more: `status` `hard`.

## Mistakes (`mistakes`)
A mistake in how the learner reasons (not a one-off slip) is kept as made: `wrong` (their step, verbatim), `fixed`, `rule` (why, one line), `kind`, `concept`, `source`, `count` 1, `box` 1, `next_review` tomorrow, `status` `fixing`. Search first: made again: `count` + 1, `status` `repeated`, box 1. Reviewed with a new problem with the same trap; right in box 3 or more: `status` `fixed`.

## Hard concepts
`status` `hard` is not learned by asking again the same way: a new explanation (another angle, a picture, an everyday analogy), a contrast with what it is confused with, then a short #practice on it alone.

## Difficulty
About 70-85% right is the best band. Under 60%: more recall and smaller steps, fewer new concepts; over 90% twice: harder kinds.

## Numbers
`python3 .claude/skills/roadmap/leitner.py stats` gives the boxes, what is due and the weak items, worked out from the data (skill `roadmap`).
