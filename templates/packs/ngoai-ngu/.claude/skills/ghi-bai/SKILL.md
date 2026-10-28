---
name: ghi-bai
description: Record a language learner's new words, grammar patterns and lessons in the notebook (tables tu_vung, ngu_phap and files in bai-hoc/), with one tagging convention so they can be reviewed and tested later. Use whenever the learner sends new words, sentences, passages or material.
---

# Recording a lesson

The notebook has two parts: **data tables** (one record per word or grammar pattern, filtered by tag, box and review date) and **lesson files** (read again like class notes). The tables are the source of truth; a lesson file points to the same items.

## 1. Pick what is worth learning
- From what the learner sends: single words, phrases, idioms and fixed expressions (collocations) are each one `tu_vung` item.
- From sentences and passages: pick new words and grammar patterns worth learning at the level in `docs/ho-so.md`; skip what the learner surely knows (ask when unsure).
- Each grammar pattern is one `ngu_phap` item (the structure, not each sentence).

## 2. No duplicates
`data_query` the `tu_vung` table with `search` set to the word (and `language`). Already there: add the new meaning, example or tags instead of a new item. Homonyms with different meanings are separate items, told apart in `meaning`.

## 3. Table `tu_vung` (data_upsert)
| Field | What to write |
|---|---|
| `word` | The word or phrase, base form (infinitive, singular) |
| `language` | Language code: `en`, `ja`, `zh`, `ko`, `fr`, `de`… |
| `reading` | Pronunciation: IPA, pinyin with tones, furigana, romaja |
| `meaning` | Meaning in the learner's language, short; numbered when several |
| `part_of_speech` | `noun`, `verb`, `adj`, `adv`, `phrase`, `idiom`, `character` (one kanji/hanzi, skill `luyen-ky-nang`), `other` |
| `example` / `example_meaning` | One natural example sentence at the learner's level, and its meaning |
| `notes` | Collocations, synonyms/antonyms, nuance, irregular forms, easily confused with… |
| `level` | The word's level: `A1`…`C2`, `N5`…`N1`, `HSK1`…`HSK6`, `TOPIK1`…`TOPIK6` |
| `topics` | Topics (see the tagging convention) |
| `tags` | Other tags (see the tagging convention) |
| `source` | Where it came from: book, article, film, link |
| `lesson` | The lesson file, e.g. `bai-hoc/2026-10-28-du-lich.md` |
| `status` | `moi` when just recorded |
| `box` | `1` when just recorded |
| `next_review` | Tomorrow (`YYYY-MM-DD`) |

Table `ngu_phap` likewise: `pattern` (the structure, e.g. `V-ている`, `have + V3`), `meaning`, `structure` (how it is formed, conjugated), `examples` (2-3 sentences with their meaning), `notes` (when to use it, how it differs from close patterns), `level`, `topics`, `tags`, `lesson`, `status`, `box`, `next_review`.

## 4. Tagging convention
Tags are lowercase ASCII, words joined by `-`. Two fields:
- **`topics`**: what the item is about; reuse one of these before making a new one: `chao-hoi` (greetings), `gia-dinh` (family), `cong-viec` (work), `du-lich` (travel), `an-uong` (food), `mua-sam` (shopping), `suc-khoe` (health), `giao-duc` (education), `cong-nghe` (technology), `moi-truong` (environment), `xa-hoi` (society), `kinh-te` (economy), `cam-xuc` (feelings), `thoi-gian` (time), `nha-cua` (home), `giao-thong` (transport), `the-thao` (sport), `van-hoa` (culture).
- **`tags`**: prefixed tags, to filter reviews and tests:
  - `bai:<YYYY-MM-DD>` the session that recorded it (required)
  - `nguon:<sach|phim|bao|podcast|hoi-thoai|de-thi>` kind of source (book, film, news, podcast, conversation, exam)
  - `ky-thi:<ielts|toeic|jlpt|hsk|topik|delf|…>` when the item is in the target exam's scope
  - `ky-nang:<nghe|noi|doc|viet>` when tied to a skill (listening, speaking, reading, writing); `chu` for a character
  - `loi:<kind>` added by the grader when the learner gets it wrong: `loi:nghia` (meaning), `loi:chinh-ta` (spelling), `loi:cach-doc` (pronunciation), `loi:chia-dong-tu` (conjugation), `loi:gioi-tu` (preposition/particle), `loi:trat-tu` (word order), `loi:nham-tu-gan` (confused with a close word), `loi:nghe-nham` (misheard)
  - `can-kiem` when the meaning or example is uncertain (the reviewer checks it, then removes the tag)
  - `uu-tien` when the learner wants it reviewed more

## 5. Lesson file
One file per session, `bai-hoc/<YYYY-MM-DD>-<topic>.md` (more on the same day goes into the same file). Headings in the learner's language:

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
```

## 6. Tell the learner
How many words and which grammar patterns were recorded, in which file; which words were already there (and what was added); what is still uncertain.
