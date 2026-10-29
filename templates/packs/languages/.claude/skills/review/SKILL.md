---
name: review
description: Spaced-repetition review for a language learner (Leitner boxes): pick the words and grammar patterns due from tables vocabulary, grammar by tag, write varied review exercises, grade them and update box, review date and mistake tags. Use for the daily review, when the learner asks to review, or when grading a review.
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

- Right: up one box (at most 5), `next_review` = today + the new box's interval. Right twice in a row in box 5: `status` = `mastered` (mastered).
- Wrong: back to box 1, `next_review` = tomorrow, `status` = `learning` (learning), add the tag `error:<kind>`. Wrong 3 times or more (`wrong` ≥ 3): `status` = `hard` (hard).
- Nearly right (a small spelling slip, a missing tone mark): keep the box, review again in 1 day.
- Always update `last_review` and `correct` or `wrong`.

## What is weak, at a glance
- Not remembered yet: `status` `learning` with `box` 1-2.
- Weak: `status` `hard` (wrong 3 times or more), or a tag `error:*`.
- Sentences got wrong: the table `mistakes` (below), `status` not `fixed`.

## Mistake log (`mistakes`)
A mistake in a sentence (in a review, writing, a role-play, a test, the chat) is kept as it was made, so it is practised until it is fixed, not just noted.
- Who writes it: whoever corrects it (the Grader in #grade, the Teacher in a role-play, a writing correction or the chat). Slips of a single word already in `vocabulary` only update the word; a mistake about how a sentence is built (particle, tense, word order, a wrong collocation, a pattern used wrongly) goes in `mistakes`.
- Search first (`data_query` `mistakes`, same `kind` and `item`, or `search` the corrected sentence): already there: `count` + 1, `status` `repeated` (made again), `box` 1, `next_review` tomorrow, the new `wrong` sentence added to `notes`. Else a new record.
- Fields: `date`, `wrong` (the learner's sentence, verbatim), `fixed` (the corrected one), `rule` (why, in one line), `kind` (`error:*` without the prefix: `preposition`, `word-order`, `conjugation`…), `item` (`vocabulary: <word>` or `grammar: <pattern>` when it is about one; tag that item `error:<kind>` too), `skill` (where it was made), `source` (the file), `count` 1, `box` 1, `next_review` tomorrow, `status` `fixing` (being fixed).
- Reviewed like items (boxes, `next_review`), but never by showing the same sentence again only: ask for the fix of a new sentence with the same mistake, or a sentence the learner makes with the same structure. Right in box 3 or more: `status` `fixed` (fixed); it stays for the record.

## Hard items (leeches)
An item wrong 3 times or more (`status` `hard`) is not learned by asking it again the same way. Before it comes back: give it a new hook (a mnemonic, a picture in words, the character's parts), a fresh example close to the learner's life, and when it is confused with another item, a contrast pair asked together. Note what was tried in `notes`.

## Picking the items
1. Due: `data_query` `vocabulary` with `next_review` `lte` today and `status` `ne` `mastered`, sorted by `next_review`. Same for `grammar`, and `mistakes` with `status` `ne` `fixed`.
2. First: tag `priority`, `status` `hard`, tags `error:*`, `mistakes` with `count` ≥ 2 or `status` `repeated`. A session keeps about a third for these weak items when there are enough.
3. The learner picks a scope ("review travel", "review last week"): filter by `topics` (`contains`), tag `lesson:<date>`, `level`.
4. One session is 15-25 items (by the daily time in `docs/profile.md`); too many due: the longest overdue first, the rest tomorrow.
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
- Listening, speaking, reading, writing practice: the skill `skills-practice`

## Difficulty
Aim for about 70-85% right in a session: some forgetting between reviews is what makes it stick. Under 60%: next session more recognition questions and fewer new items; over 90% twice in a row: more production questions and sentences.

## Files and tables
- Exercise: `exercises/<YYYY-MM-DD>-review.md`; answers: `exercises/<YYYY-MM-DD>-review-answers.md`. In the answer file, each question names the item it tests: `(vocabulary: <word>)`, `(grammar: <pattern>)` or `(mistakes: <id>)`.
- A `exercises` record: `kind` = `review`, `file`, `items`, `status` = `todo` (not done).
- Put the questions in the chat in order, without answers. The learner answers in the chat or in the exercise file.

## After grading
Sum up: how many right, items that moved up, items back in box 1 with their mistake, mistakes fixed or made again (`mistakes`), weak tags (`weak_tags`), how many items are due tomorrow.
