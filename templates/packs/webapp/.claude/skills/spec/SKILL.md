---
name: spec
description: How to write a feature spec for the web app (problem, flow, acceptance criteria, out of scope, data and API changes, risks) and how to propose a stack for a new app. Use before building anything bigger than an hour of work, when the owner asks for a new feature, or when the repo has no code yet.
---

# Spec

## A feature spec
Write `docs/specs/<YYYY-MM-DD>-<slug>.md` with these sections, short:

1. **Problem**: who has it, what they cannot do today, why it matters (one paragraph).
2. **Flow**: the steps the user takes, screen by screen or call by call.
3. **Acceptance criteria**: a numbered list, each checkable by a test or in a browser: "Given …, when …, then …". Cover the states: loading, empty, error, no permission, success; mobile.
4. **Out of scope**: what this spec does not do (so nobody builds it).
5. **Data and API**: new or changed tables, fields, endpoints, with their input, output and errors. A migration for every schema change; what happens to old data.
6. **Files**: the routes, components, models and tests it touches (read the code first, name real paths).
7. **Risks**: security (auth, input, secrets), performance (lists, queries), what is hard to undo.
8. **Open questions**: what only the owner can answer. Never guess these.

Keep it to what the feature needs now; no design for imagined needs. Record it in `features` (`spec`, `acceptance`, `status` `spec`).

## A new app: proposing a stack
- Ask the owner what the app does, for whom, how many users, where it will run, the budget (free only?), and what they already know.
- Propose the simplest stack that fits, with one alternative and the trade-off. Defaults when nothing argues otherwise: a full-stack framework with server rendering (Nuxt or Next.js), TypeScript, SQLite or Postgres, a test runner of the framework (Vitest/Jest) and Playwright for the browser.
- Write it in `docs/stack.md` (the commands too) and the choice in `decisions` after the owner's yes.

## Help from the office library
`website-blueprint` (site structure, pages) and `research-brief` (research on users or competitors): find them with find_skills, read one before using it.
