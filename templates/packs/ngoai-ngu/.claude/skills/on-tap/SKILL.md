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

## Hard items (leeches)
An item wrong 3 times or more (`status` `kho`) is not learned by asking it again the same way. Before it comes back: give it a new hook (a mnemonic, a picture in words, the character's parts), a fresh example close to the learner's life, and when it is confused with another item, a contrast pair asked together. Note what was tried in `notes`.

## Picking the items
1. Due: `data_query` `tu_vung` with `next_review` `lte` today and `status` `ne` `thuoc`, sorted by `next_review`. Same for `ngu_phap`.
2. First: tag `uu-tien`, `status` `kho`, tags `loi:*`.
3. The learner picks a scope ("review travel", "review last week"): filter by `topics` (`contains`), tag `bai:<date>`, `level`.
4. One session is 15-25 items (by the daily time in `docs/ho-so.md`); too many due: the longest overdue first, the rest tomorrow.
5. A busy day (the learner says so, or the day's time is under 10 minutes): the short session, the 10 most overdue items only, so the habit holds; a short day beats a skipped one.
6. Mix them (interleaving): words and grammar, topics and sessions in one exercise, never one topic in a block.

## Exercise types
Recall, not recognition, whenever the item allows: the learner produces the answer. Mix them so it sticks, one question per item, harder as the box goes up (boxes 1-2: recognise and choose; 3-5: produce, use in a sentence, explain):
- Meaning → word (type the word, with its reading), word → meaning
- Fill the gap in a new sentence (never reuse the stored example)
- Multiple choice (4 options, distractors close in meaning or form)
- Conjugate / pick the right form for a grammar pattern
- Correct a wrong sentence; translate a short sentence into the target language
- Explain it back: "when do you use X rather than Y?", "make a sentence about your day with X" (grammar, boxes 3-5)
- Listening, speaking, reading, writing practice: the skill `luyen-ky-nang`

## Difficulty
Aim for about 70-85% right in a session: some forgetting between reviews is what makes it stick. Under 60%: next session more recognition questions and fewer new items; over 90% twice in a row: more production questions and sentences.

## Files and tables
- Exercise: `bai-tap/<YYYY-MM-DD>-on-tap.md`; answers: `bai-tap/<YYYY-MM-DD>-on-tap-dap-an.md`. In the answer file, each question names the item it tests: `(tu_vung: <word>)` or `(ngu_phap: <pattern>)`.
- A `bai_tap` record: `kind` = `on-tap`, `file`, `items`, `status` = `chua-lam` (not done).
- Put the questions in the chat in order, without answers. The learner answers in the chat or in the exercise file.

## After grading
Sum up: how many right, items that moved up, items back in box 1 with their mistake, weak tags (`weak_tags`), how many items are due tomorrow.
