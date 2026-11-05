---
name: knowledge-base
description: Write and fix the support knowledge base (playbooks in docs/kb/) from repeated customer questions, from the policies only. Use for #weekly-faq, when a question has no playbook, or when a playbook was wrong.
---

# Knowledge base

## One file per question
`docs/kb/<slug>.md`, slug in lowercase ASCII (`doi-tra-size.md`):

```markdown
# <The question as customers ask it>
Also asked as: <2-4 other ways customers say it>
Case: <case type key>

## Answer
<The answer in the business's voice, 2-6 sentences; steps as a list>

## Conditions
<When it applies, when it does not; what needs a person>

Source: docs/policies.md, section <…> · Updated: <YYYY-MM-DD>
```

## Rules
- Facts only from `docs/policies.md` (or a decision the owner wrote in `escalations`). A fact missing: put the question in the gaps list for the owner, do not write the playbook yet.
- Search first (`support_kb_search`): an existing playbook is fixed, never duplicated.
- Words customers use go in "Also asked as": that is what makes search find it.
- No customer names, emails, phone numbers or order numbers in a playbook.
- A policy changed: every playbook citing that section is checked the same day.

## Which questions
The week's groups by count; first those the AI could not answer, those a person edited, those escalated without need. Three or more in a week: it gets a playbook.
