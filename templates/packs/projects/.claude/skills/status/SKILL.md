---
name: status
description: Follow up tasks and report project status - due and overdue tasks by owner as a draft message, milestone forecasts from the tasks left, the weekly status report. Use for the daily follow-up, the weekly status or when the lead asks how things are going.
---

# Status

## Follow-ups
- `tasks` with `status` not `done`/`dropped` and `due` within 3 working days, and those overdue; grouped by owner, one line each (task, due, project).
- As a message the lead can send, friendly and short; never sent by you.

## Milestone forecast
- From the tasks left on it, their owners' pace (tasks done per week lately) and what is blocked. A forecast is labelled as one.
- Health: `on_track` (forecast on or before the date), `at_risk` (within the buffer, or a task on the critical path late or blocked), `late` (after the date).

## Weekly status (`reports/<YYYY>-W<week>-status.md`)
| Project | Milestone | Date | Forecast | Health | Why |
|---|---|---|---|---|---|

Then: done this week; slipping (why); blocked (by what, who can unblock); next week; decisions the lead must take. Update each `projects` record's `health` and `forecast`.

## Standards
Facts from the records and the lead's updates; bad news early, with a proposal (cut scope, move the date, add help).
