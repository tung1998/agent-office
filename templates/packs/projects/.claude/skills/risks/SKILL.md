---
name: risks
description: Keep a project risk register and challenge plans - likelihood and impact, level, owner, response, triggers, pre-mortem, risks become issues; PASS/FAIL review of a plan. Use when planning, at the risk review, or when a milestone slips.
---

# Risks

## The register (`risks`)
Each risk: what could happen and its effect ("supplier late → launch slips 2 weeks"), `likelihood` and `impact` (low, medium, high), `level` (high when either is high and the other at least medium), an `owner`, a `response` (avoid, reduce, transfer, accept) with its `plan`, the `trigger` that says it is happening, `reviewed_on`.

## Finding risks
- Pre-mortem: "it is the deadline and the milestone failed: why?" for each milestone in the next 4 weeks.
- The usual ones: a key person away or overloaded, a dependency on another team or supplier, an approval, budget, unclear scope or scope creep, a new technology, data or access not ready.
- A risk that happened is an issue (`status` `issue`): it needs an action and an owner now, in `tasks`.

## Reviewing a plan
- Outcome measurable; milestones are results with dates; every task one owner, a date, done means.
- Dependencies and outside lead times present; the critical path known.
- People's time within what `docs/team.md` says; a buffer of 15-20%.
- First line PASS or FAIL; then each point: what, why it matters, the fix.
