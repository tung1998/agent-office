---
name: skills-practice
description: Practise each skill of a language, not only words and grammar: reading (graded passages), listening (real audio found on the web, dictation, shadowing), speaking (role-play in the chat, shadowing, voice-to-text), writing (prompts, corrections, a rewritten version) and characters (kanji, hanzi, hangul). Use for a roadmap task of kind nghe, noi, doc, viet or chu, when the learner asks to practise a skill, or when a skill is weak.
---

# Skills practice

Words and grammar are the notebook (skills `notes`, `review`); this skill uses them. Every practice ends with: what went well, each mistake with its fix (kept in `mistakes`, skill `review`), the new words and patterns worth recording (skill `notes`, tag `skill:<skill>`), and the `exercises` record (`kind` `practice`, `skill`, score when there is one, `weak_tags`). A roadmap task: set its `schedule` `status` and `result`.

## Level of the material
Material is at the learner's level plus a little (about 90-95% known words): count against `vocabulary` (`status` not `new`) when choosing. Mostly topics they care about (profile, memory `learner`), the target exam's topics when the plan is `exam`.

## Reading (doc)
- A passage written at the level (or a short real text with its source), 150-400 words by level, saved in `exercises/<YYYY-MM-DD>-reading-<topic>.md`; questions in order: gist, details, a word's meaning from context, the writer's intent. Answers in the `…-answers.md` file.
- Time it like the exam when the plan is `exam`. After grading: the sentences they misread, explained.

## Listening (nghe)
Office cannot play or record sound: use real audio the learner opens themselves.
- Find it on the web (the Test writer has the web): free and legal sources with a transcript where possible (public broadcasters' easy-news pages, graded podcasts, official sample tests, videos with subtitles). Keep each source in `resources/listening-sources.md` (link, level, length, transcript or not, date checked) and reuse it; never download or copy the audio.
- One task: the link and the segment (start-end), 3-5 questions (gist, then details), then a dictation of 2-3 sentences. Then shadowing: listen and repeat the segment with the transcript.
- Grading: questions against the transcript; dictation word by word (mistakes tagged `error:mishearing`, misheard); the words they could not catch go to the notebook.
- Videos (YouTube and the like): give the segment as a link that opens at its start (`&t=<seconds>s` on YouTube) and tie each question to its timestamp (`02:15`), so the learner can replay just that part. Subtitles in the target language when the level allows, none on a second listen. A long video spread over several days: keep where they stopped in `resources/listening-sources.md` (column Transcript or a note) and start the next task there.

## Speaking (noi)
- **Role-play** in the chat: a situation at the level (ordering food, a job interview, the exam's speaking task), you play the other person, 6-10 turns. The learner speaks with voice-to-text on their phone or keyboard (what it wrote shows how they were heard: a wrong word there is often a pronunciation slip) or types.
- **Shadowing / reading aloud**: a short script with its reading (IPA, pinyin with tones, furigana); the learner records themselves with any app and checks against the self-check list (sounds, tones, pitch accent, linking, pace), or reads it to voice-to-text and pastes what it wrote.
- Feedback after the role-play, not in the middle: the corrected sentences, a more natural way to say it, the pronunciation points (`error:reading`). Exam speaking: grade by the exam's criteria in `resources/exam-formats.md`.

## Writing (viet)
- A prompt at the level or in the exam's format (a message, an email, an opinion essay, 作文), with the word count and time.
- Correct in three parts: each error marked with its fix and its kind (`error:*`), a rewritten version at the learner's level (not much higher), and two or three phrases worth keeping. Exam writing: band or score by the exam's criteria.
- The same mistake twice: a short focused exercise on it next time.

## Characters (chu) - Japanese, Chinese, Korean
- Each character is a `vocabulary` item with `part_of_speech` `character`: `word` the character, `reading` its readings (on/kun, pinyin), `meaning`, `notes` the radical, stroke count and 2-3 common words with it (recorded as words too), `tags` `characters`. Reviewed like words (skill `review`): character → reading and meaning, word → write it.
- Writing by hand: the learner writes on paper and checks the stroke order; photos may be sent for a look.
- Hangul (Korean) is learned in the first days, not tracked item by item; after that, `characters` tasks are spelling and batchim.

## Balance
The roadmap shares time between skills by the kind of plan (skill `roadmap`); outside a roadmap, cover every skill at least once a week, the weakest one twice.
