---
name: analytics
description: Read the TikTok numbers the creator gives (pasted or exported from TikTok Studio), record them per video, compare by hook, format, pillar and length, and keep lessons the next ideas read. Use for #weekly-review, the weekly plan, or when the creator asks how the channel is doing.
---

# Numbers and lessons

Office cannot read TikTok: the creator pastes the numbers or drops an export (CSV) in `data/`. Never guess a number that was not given.

## 1. Record
Per posted video, in its `videos` record: `views`, `avg_watch_s`, `completion_pct` (watched full video %), `likes`, `comments`, `shares`, `saves`, `follows`. Match by `posted_url` or title; a video not found: ask.

## 2. Read
- Per video: **retention first** (average watch time / length, completion %), then shares and saves (people found it worth keeping or sending), then follows per 1,000 views; likes last.
- Rough guides only, the channel's own median matters more: completion above ~50% on a 15-30 s video is good; a big drop in the first 2 s means the hook or the first frame failed; shares + saves above ~2% of views is strong.
- Compare groups by `hook` pattern, `format`, `pillar`, `length_s` and posting time against the channel's median of the last 4 weeks. Under 10 videos in a group: say it means little yet.
- Best and worst video of the week: why, from the numbers and the video itself (hook, first frame, pacing, topic), not from a guess.

## 3. Report (`reports/<YYYY>-W<week>.md`)
A table of the week's videos, the week against the last (views, average watch time, completion, follows), best and worst with why, the lessons, next week's plan (3 lines) which also goes into `docs/calendar.md`.

## 4. Lessons (`lessons`)
- One record: `date`, `lesson` (one sentence the next idea or script can follow), `evidence` (the videos and numbers), `tags` (`hook:<pattern>`, `format:<format>`, `pillar:<pillar>`, `length:<range>`), `status` `new`.
- Search `lessons` first: the same lesson again = `count` + 1. Held 3 times = `status` `rule`; propose the line for `docs/channel.md` or the skill it belongs to, for the creator to agree.
- A lesson the numbers later contradict: `status` `dropped`, with why.
- Good videos teach too: what worked, when it worked more than once.
