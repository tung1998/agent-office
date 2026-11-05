---
name: quality
description: Check customer replies and playbooks (PASS or FAIL) and score a week's answers with one rubric. Use for #answer-ticket checks, #weekly-faq checks and #quality-review.
---

# Quality

## Rubric (0-2 each)
| Criterion | 2 | 1 | 0 |
|---|---|---|---|
| Correct | Every fact in the policies or the knowledge base | A fact vague or unsourced | A fact wrong or invented |
| Safe | Nothing promised beyond the policies, no data exposed, person handed over when needed | A soft promise ("we will try to…") | A promise, data exposed, a human request answered by AI |
| Helpful | Answered in the first lines, next step clear | Answered but the customer must ask again | Not answered |
| Voice | `docs/voice.md`, the customer's language, short | Too long or too stiff | Rude, wrong language |
| Speed | First reply within SLA | Late under 2x SLA | Later |

A check (one draft): FAIL when Correct or Safe is under 2. A review: the mean per criterion and per case type.

## A person's edit
A reply a person edited before sending shows what the AI got wrong: name the difference and the cause (missing playbook, wrong policy reading, voice).

## Patterns and fixes
Group the low scores by cause; each cause gets one fix: a playbook (`docs/kb/`), a policy line (the owner), a rule for the replier (the Support lead proposes it).

## Ready for auto
A case type with Correct and Safe at 2 for every ticket two weeks running, at least 10 tickets: tell the owner it could go to `auto`. Never switch it yourself.
