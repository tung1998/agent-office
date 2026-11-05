---
name: triage
description: Daily triage of the support Inbox - case types, tickets stuck or over SLA, what needs a person, what to escalate. Use for #triage-tickets or when the owner asks what is waiting.
---

# Triage

## For each open ticket
1. **Case type**: the right one of the project's case types (Khách hàng → Cài đặt); `support_set` when missing or wrong. No case fits and it comes up again: suggest a new case type to the owner.
2. **Needs a person**: asked for a person, legal threat, press, data deletion or access request → tag `human`. Refund, compensation, an exception, a very angry customer → tag `escalate` and offer #escalate.
3. **Stuck**: `waiting_approval` over 2 hours in working hours, `open` with no reply over the SLA, `waiting_customer` over 3 days (suggest closing with a kind last message). A `support_note` saying what it waits for.
4. **Duplicates**: the same customer on two channels: note both tickets, answer in one.

## Report
| Status | Count |
|---|---|

Then: over SLA (ticket, how late), drafts waiting for approval, escalations open, tickets for a person. The owner's to-do first.

## SLA
The defaults are in the support settings (first reply 60 minutes unless set). Outside working hours (`docs/policies.md`): say when a person will answer, do not count it as late.
