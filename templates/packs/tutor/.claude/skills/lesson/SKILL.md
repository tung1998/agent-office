---
name: lesson
description: Turn a learner's material (a chapter, slides, a link, photos of notes) or a topic into a lesson at their level, and record each new concept for spaced review in table concepts. Use when the learner sends material or asks to learn a topic, and in #make-lesson.
---

# A lesson

## 1. What to teach
- From the material: the 3-5 ideas that matter most for the learner's goal (`docs/profile.md`); the rest is named for next time. A lesson takes about the day's time.
- Prerequisites first: `data_query` `concepts` for what it builds on. Known (box 3 or more): use it and say so ("như bài đạo hàm"). Missing: a short refresher first, or a separate lesson.

## 2. The file `lessons/<YYYY-MM-DD>-<topic>.md`
```markdown
# <Topic> · <date>
Nguồn: <the material, pages>
## Mục tiêu
After this lesson you can … (2-3 lines, checkable)
## Ý chính
Each idea: plain words first, then the formal statement (LaTeX), then why it is true or where it comes from.
## Ví dụ có lời giải
2-3 worked examples, easy to harder, every step written; code runs as shown (run it).
## Hay nhầm
The usual mistakes and how to tell.
## Tự kiểm tra
3-5 questions; answers after a `---` line at the end.
## Nhật ký
- Learned / Hard / Confidence 1-5 (the learner's words at the end of the session)
```
Explain the way the profile and memory `learner` say (examples first, visual, short). A picture helps: a Mermaid diagram or an ASCII sketch.

## 3. Record the concepts (`concepts`, data_upsert)
One record per concept, formula, method or skill worth reviewing: `concept` (short name), `subject`, `topic`, `explanation` (one or two lines in the learner's words), `example` (a small one, not the lesson's main example), `prerequisites`, `notes` (confused with…), `level`, `tags` (`lesson:<YYYY-MM-DD>`, `exam:<exam>` when in scope), `source`, `lesson` the file, `box` 1, `next_review` tomorrow, `status` `new`. Search first (`search` the name): already there: add the new example or note instead.

## 4. Facts checked
Definitions, formulas, dates and APIs from the learner's material first, then a trustworthy source; a doubt is written as a doubt. #make-lesson has a second agent check it.
