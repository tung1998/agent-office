---
name: planning
description: Turn a goal into a project plan - measurable outcome, milestones by result, tasks with one owner, due date and definition of done, dependencies, buffer, first risks. Use when a project starts, a goal is given, or a plan is re-made after a big change.
---

# Planning

## 1. The outcome
One or two sentences: what is true when the project is done, and how it is measured (a number, a launch, a signed document). "Improve X" is not an outcome; "X from 40% to 60% by 30/6" is.

## 2. Milestones by result
Work back from the deadline: 3-7 milestones, each a result someone can check ("beta used by 5 customers"), not an activity ("work on beta"). Each with a date and an owner.

## 3. Tasks
Per milestone, the tasks to reach it: a verb, one owner (a person, never "the team"), a due date, what done means. Tasks over a week are split. Lead times outside the team (approvals, suppliers, other teams) are tasks too, with their wait.

## 4. Is it possible
- The people's time from `docs/team.md` against the work: someone over 100% is a risk, not a plan.
- Dependencies: the chain that sets the end date (the critical path); a task on it that slips moves the deadline.
- A buffer of 15-20% before the deadline, kept as a buffer, not filled with work.
- Not possible: say so with the numbers and offer choices (cut scope, move the date, add people).

## 5. Write it
`plans/<YYYY-MM-DD>-<slug>.md`: outcome and measure, a milestones table (milestone, owner, date, depends on), the tasks per milestone, the buffer, the first risks (skill `risks`), `## Changes`. Then the records: `projects`, `tasks`, `risks`.
