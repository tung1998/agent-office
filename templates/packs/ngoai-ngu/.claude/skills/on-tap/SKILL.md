---
name: on-tap
description: Spaced-repetition review for a language learner (Leitner boxes): pick the words and grammar patterns due from tables tu_vung, ngu_phap by tag, write varied review exercises, grade them and update box, review date and mistake tags. Use for the daily review, when the learner asks to review, or when grading a review.
---

# Review

## Review schedule (Leitner boxes)
Each item has a `box` 1-5 and a `next_review`.

| Box | Review again after |
|---|---|
| 1 | 1 day |
| 2 | 3 days |
| 3 | 7 days |
| 4 | 14 days |
| 5 | 30 days |

- Right: up one box (at most 5), `next_review` = today + the new box's interval. Right twice in a row in box 5: `status` = `thuoc` (mastered).
- Wrong: back to box 1, `next_review` = tomorrow, `status` = `dang-hoc` (learning), add the tag `loi:<kind>`. Wrong 3 times or more (`wrong` ≥ 3): `status` = `kho` (hard).
- Nearly right (a small spelling slip, a missing tone mark): keep the box, review again in 1 day.
- Always update `last_review` and `correct` or `wrong`.

## Picking the items
1. Due: `data_query` `tu_vung` with `next_review` `lte` today and `status` `ne` `thuoc`, sorted by `next_review`. Same for `ngu_phap`.
2. First: tag `uu-tien`, `status` `kho`, tags `loi:*`.
3. The learner picks a scope ("review travel", "review last week"): filter by `topics` (`contains`), tag `bai:<date>`, `level`.
4. One session is 15-25 items (by the daily time in `docs/ho-so.md`); too many due: the longest overdue first, the rest tomorrow.

## Exercise types
Mix them so it sticks, one question per item:
- Meaning → word (type the word, with its reading), word → meaning
- Fill the gap in a new sentence (never reuse the stored example)
- Multiple choice (4 options, distractors close in meaning or form)
- Conjugate / pick the right form for a grammar pattern
- Correct a wrong sentence; translate a short sentence into the target language
- Listening/speaking: a sentence to read aloud, or a dictation (marked as self-assessed)

## Files and tables
- Exercise: `bai-tap/<YYYY-MM-DD>-on-tap.md`; answers: `bai-tap/<YYYY-MM-DD>-on-tap-dap-an.md`. In the answer file, each question names the item it tests: `(tu_vung: <word>)` or `(ngu_phap: <pattern>)`.
- A `bai_tap` record: `kind` = `on-tap`, `file`, `items`, `status` = `chua-lam` (not done).
- Put the questions in the chat in order, without answers. The learner answers in the chat or in the exercise file.

## After grading
Sum up: how many right, items that moved up, items back in box 1 with their mistake, weak tags (`weak_tags`), how many items are due tomorrow.
