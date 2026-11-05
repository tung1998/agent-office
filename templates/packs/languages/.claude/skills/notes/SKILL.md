---
name: notes
description: Record a language learner's new words, grammar patterns and lessons in the notebook (tables vocabulary, grammar and files in lessons/), with one tagging convention so they can be reviewed and tested later. Use whenever the learner sends new words, sentences, passages or material.
---

# Recording a lesson

The notebook has two parts: **data tables** (one record per word or grammar pattern, filtered by tag, box and review date) and **lesson files** (read again like class notes). The tables are the source of truth; a lesson file points to the same items.

## 1. Pick what is worth learning
- From what the learner sends: single words, phrases, idioms and fixed expressions (collocations) are each one `vocabulary` item.
- From sentences and passages: pick new words and grammar patterns worth learning at the level in `docs/profile.md`; skip what the learner surely knows (ask when unsure).
- Each grammar pattern is one `grammar` item (the structure, not each sentence).

## 2. No duplicates
`data_query` the `vocabulary` table with `search` set to the word (and `language`). Already there: add the new meaning, example or tags instead of a new item. Homonyms with different meanings are separate items, told apart in `meaning`: 马 "horse" and 马 "the surname Mã" are two items. So a search hit is "already recorded" only when the meaning matches too: compare `meaning` before saying so, and record the other meaning as its own item.

## 3. Table `vocabulary` (data_upsert)
| Field | What to write |
|---|---|
| `word` | The word or phrase, base form (infinitive, singular) |
| `language` | Language code: `en`, `ja`, `zh`, `ko`, `fr`, `de`… |
| `reading` | Pronunciation: IPA, pinyin with tones, furigana, romaja |
| `meaning` | Meaning in the learner's language, short; numbered when several |
| `part_of_speech` | `noun`, `verb`, `adj`, `adv`, `phrase`, `idiom`, `character` (one kanji/hanzi, skill `skills-practice`), `other` |
| `example` / `example_meaning` | One natural example sentence at the learner's level, and its meaning |
| `notes` | Collocations, synonyms/antonyms, nuance, irregular forms, easily confused with…; the textbook's own gloss when it differs from common usage (below) |
| `level` | The word's level: `A1`…`C2`, `N5`…`N1`, `HSK1`…`HSK6`, `TOPIK1`…`TOPIK6` |
| `topics` | Topics (see the tagging convention) |
| `tags` | Other tags (see the tagging convention) |
| `source` | Where it came from: book, article, film, link |
| `lesson` | The lesson file, e.g. `lessons/2026-10-28-travel.md` |
| `status` | `new` when just recorded |
| `box` | `1` when just recorded |
| `next_review` | Tomorrow (`YYYY-MM-DD`), unless new items are paused (below) |

**Textbook glosses.** Some books gloss a word differently from common usage (a regional or old meaning, or a local equivalent: 兔 glossed "cat" in Vietnamese zodiac books, 牛 "buffalo", 羊 "goat"). Write the common meaning in `meaning`, the book's gloss in `notes` ("textbook: …"), and tag `to-check` when unsure which is right. Say it to the learner.

**Characters: no stroke details from memory.** For a `character` item, the radical, stroke count and stroke order in `notes` come from a source you checked (a dictionary site with stroke-order data, the learner's book); never describe stroke shapes or order from memory. Unverified: write "unverified" in `notes`, tag `to-check` and ask the learner to check the book.

**New items paused.** When the roadmap pauses new material (skill `roadmap` section 5: the review backlog is over a day's session), still record what the learner sends, but do not put it in review tomorrow: set `next_review` to the date the plan resumes new items (the backlog cleared, from the roadmap's week table), add the tag `paused`, and say so in the reply ("recorded 12 words, held until 24/11 so the backlog can clear"). Offer to move the date if the learner wants to start on them now. When new items resume, the weekly check (skill `roadmap`) clears `paused` from the items now due.

Table `grammar` likewise: `pattern` (the structure, e.g. `V-ている`, `have + V3`), `meaning`, `structure` (how it is formed, conjugated), `examples` (2-3 sentences with their meaning), `notes` (when to use it, how it differs from close patterns), `level`, `topics`, `tags`, `lesson`, `status`, `box`, `next_review`.

## 4. Tagging convention
Tags are lowercase ASCII, words joined by `-`. Two fields:
- **`topics`**: what the item is about; reuse one of these before making a new one: `greetings` (greetings), `family` (family), `work` (work), `travel` (travel), `food` (food), `shopping` (shopping), `health` (health), `education` (education), `technology` (technology), `environment` (environment), `society` (society), `economy` (economy), `feelings` (feelings), `time` (time), `home` (home), `transport` (transport), `sports` (sport), `culture` (culture).
- **`tags`**: prefixed tags, to filter reviews and tests:
  - `lesson:<YYYY-MM-DD>` the session that recorded it (required)
  - `source:<sach|phim|bao|podcast|hoi-thoai|exam>` kind of source (book, film, news, podcast, conversation, exam)
  - `exam:<ielts|toeic|jlpt|hsk|topik|delf|…>` when the item is in the target exam's scope
  - `skill:<listening|speaking|reading|writing>` when tied to a skill (listening, speaking, reading, writing); `characters` for a character
  - `error:<kind>` added by the grader when the learner gets it wrong: `error:meaning` (meaning), `error:spelling` (spelling), `error:reading` (pronunciation), `error:conjugation` (conjugation), `error:preposition` (preposition/particle), `error:word-order` (word order), `error:confusable` (confused with a close word), `error:mishearing` (misheard)
  - `to-check` when the meaning or example is uncertain (the reviewer checks it, then removes the tag)
  - `priority` when the learner wants it reviewed more
  - `paused` when recorded while new items are paused (section 3); removed when it comes due

## 5. Lesson file
One file per session, `lessons/<YYYY-MM-DD>-<topic>.md` (more on the same day goes into the same file). Headings in the learner's language:

```markdown
# <Topic> · <date>
Source: <…>

## Vocabulary
| Word | Reading | Meaning | Example |
|---|---|---|---|

## Grammar
### <pattern>
Structure, meaning, examples, how it differs from…

## Notes
Culture, usage, common mistakes.

## Self-check
3-5 quick questions (answers at the end of the file, after a `---` line).

## Journal
- Learned: <one line>
- Hard: <what did not stick or was confusing>
- Confidence: <1-5>
```

The journal is written at the end of each session, three short lines in the learner's words (ask them; write it yourself only if they skip it, and say so). Every session that day adds its own lines.

## 6. Tell the learner
How many words and which grammar patterns were recorded, in which file; which words were already there (and what was added); what is still uncertain.
