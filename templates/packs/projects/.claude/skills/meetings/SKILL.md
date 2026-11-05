---
name: meetings
description: Turn raw meeting notes or a transcript into short minutes, decisions with who decided, action items with one owner and a due date in table tasks, open questions and a recap message. Use whenever the lead shares meeting notes, a transcript or a recording's text. The office library skill meeting-actions (find_skills) goes further on transcripts.
---

# Meetings

## Minutes (`meetings/<YYYY-MM-DD>-<slug>.md`)
```markdown
# <Meeting> · <date>
Attendees: …   Project: …

## Agenda
## Discussion
### <Topic>
2-4 lines: what was said, the options, where it landed.
## Decisions
- <decision> (decided by <who>)
## Action items
| Action | Owner | Due | Done means |
## Open questions
## Recap message (draft)
```

## Rules
- Neutral and short; nothing added that was not said. Names as in `docs/team.md`.
- A **decision** is agreed, with who decided. Discussed but not agreed: an open question.
- An **action item** has a verb, one owner, a due date, what done means. Owner or date not said: "to confirm", listed for the lead; never invented.
- Records: the `meetings` record (counts of decisions and actions, `to_confirm`), each decision in `decisions` (`source` the minutes), each action in `tasks` (search first by title and owner: update an existing task).
- The recap message: decisions, then action items grouped by owner, then what to confirm. The lead sends it, not you.

## Transcripts
Long transcripts: skim for decisions ("we'll", "let's go with", "agreed"), commitments ("I'll", "can you… by"), dates and names; the office library skill `meeting-actions` (find it with `find_skills`) has a fuller method.
