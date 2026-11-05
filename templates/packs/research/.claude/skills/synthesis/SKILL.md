---
name: synthesis
description: Turn graded sources into findings and a research report - answer first, confidence, findings tied to sources, conflicts shown, unknowns, the living notes updated. Use when writing a research report, a market scan or updating a topic page.
---

# Synthesis

## Findings (`findings`)
One claim per record, one sentence: `claim`, `sources` (ids), `confidence` (high: two or more independent A/B sources agree; medium: one good source or some disagreement; low: weak or indirect), `why`, `would_change`, `question`, `topic`.

## Weighing evidence
- Newer beats older only when the method is as good; a larger, transparent study beats a small one.
- Independent agreement counts; ten articles quoting one press release are one source.
- Correlation is not cause; a forecast is not a fact; a company's claim about itself is a claim.
- Conflicts are reported side by side with why one is stronger.

## The report (`reports/<YYYY-MM-DD>-<slug>.md`)
```markdown
# <Question>
**Answer:** <3-5 lines>  **Confidence:** high | medium | low  **Date:** <today>

## Findings
1. <claim> [1][2]
## Where sources disagree
## What we don't know
## What would change this
## Sources
[1] Publisher, "Title", published date, accessed date, link (grade)
```
Tables for comparisons; every cell sourced. Plain language, units and definitions stated.

## The living notes
Update `notes/<topic>.md` with what is new (facts with sources, key numbers, open questions, a history line); do not paste the report.
