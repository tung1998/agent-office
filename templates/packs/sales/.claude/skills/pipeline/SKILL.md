---
name: pipeline
description: Run the sales pipeline - touches, follow-up timing, sorting replies, deal stages and the weekly review with lessons. Use for #follow-up, the weekly pipeline review, or when the owner reports a send or a reply.
---

# Pipeline

## Touches
- Every send the owner reports is a `touches` record: `direction` `out`, `step` (1, 2, 3), `outcome` `sent`, `next_on`.
- Spacing: touch 2 three working days after 1; touch 3 a week after 2. No reply after 3: lead `status` `no_reply`, stop.
- Follow-ups: shorter than the first, a new reason or a useful fact (an article, a number from a source), never "just checking in", never guilt.

## Replies
| Reply | Do |
|---|---|
| Interested, a question | `deals` record, stage `talking`, next step and date; answer drafted from `docs/offer.md` |
| Not now | `next_on` in 60-90 days, the reason noted |
| No, unsubscribe, angry | Lead `do_not_contact`; no more drafts, ever |
| Wrong person | Ask who is right only if they offered; else close |

## Deals
Stages: `talking` → `demo` → `proposal` → `negotiation` → `won` or `lost` (`lost_reason`). An open deal with no touch for 14 days is stale: draft the next step.

## Weekly review
One table: leads by grade and status, touches sent, reply rate (replies / first touches), deals by stage and total value, won and lost. Then lessons in `lessons` (search first, `count` + 1; `level` `pattern` at 3, `rule` at 5): which segment, channel, angle, subject got replies, with the numbers. Under 20 sends: say the numbers mean little.
