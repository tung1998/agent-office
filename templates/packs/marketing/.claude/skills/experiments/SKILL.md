---
name: experiments
description: Design and read A/B tests on a test ladder (hook, creative, offer, audience, channel, landing page) - one change at a time, the sample needed, the stop rule, and the result with its uncertainty, kept as a lesson. Use for #ab-test and when someone asks whether one version beat another.
---

# Experiments

## The ladder
Test in this order, one step at a time, the cheapest change with the biggest effect first: **hook** (first line, headline, thumbnail) → **creative** (image, video, format) → **offer** (price, bonus, guarantee) → **audience** → **channel** → **landing page**. A winner stays; the next test starts from it.

## Design (`experiments` record, `status` `designed`)
- Hypothesis: "Changing X to Y raises <metric> because <reason from a lesson or research>".
- One change between A and B; everything else equal (audience, budget, time).
- Metric: the one closest to the goal that will have enough events (click rate when conversions are too few).
- Sample needed: for a rate p and a lift to detect, roughly `16 × p × (1 − p) / (difference)²` per variant (80% power, 5% level). Say how long that takes at the current traffic; too long: test a bigger change or a higher-traffic metric.
- Stop rule set before it starts: the sample reached or the end date; never stop early because one is ahead.

## Reading (`status` `done` or `inconclusive`)
- Per variant: events / trials and the rate. The difference and a rough 95% interval: `± 1.96 × sqrt(pA(1−pA)/nA + pB(1−pB)/nB)`.
- The interval crosses 0, or under the sample: `inconclusive`, say so; it is still a hunch for `lessons`.
- A winner: `winner`, and a `lessons` record (search first; again = `count` + 1).

## Running it
The owner runs it on the platform (split test of the ads manager, two email subject lines, two posts at similar times). Agents never run, pause or fund a test.
