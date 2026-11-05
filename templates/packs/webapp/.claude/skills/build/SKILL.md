---
name: build
description: How to build a feature or fix a bug in this web app: read before writing, test first, the repo's patterns, every UI state, the checks to run before handing back. Use whenever you change the app's code.
---

# Build

## Before writing
- `docs/stack.md`: the stack and the commands (install, dev, build, typecheck, lint, test, e2e). Missing a command: find it in `package.json` (or the build file) and add it there.
- The spec and its acceptance criteria. Then the code around the change: the nearest component, route, model and their tests. Reuse before creating.
- A library or framework API you are unsure of: read its current docs (context7 MCP when connected, else the official site). Do not guess from memory.

## While writing
- Test first: write the test, see it fail, make it pass. A bug: a test that reproduces it.
- UI: loading, empty, error and success states; works at 390px wide and with the keyboard; labels on inputs; no hard-coded text if the app has i18n.
- Server: check input at the edge, clear errors without internals, auth on every route that needs it, no N+1 queries. A schema change has its migration.
- Small steps; one concern per change. No dead code, no commented-out code.

## Secrets and dependencies
- Secrets only in the env file that stays out of git; a new variable is named (without its value) in the example env file and in `docs/stack.md`.
- A new dependency: why, its licence, its size; nothing paid without the owner's yes.

## Before handing back
Run, in order, and give each command with its result: install (if dependencies changed), build, typecheck, lint, tests. All pass, or say exactly what fails and why. Then say how to see the change: the URL, the steps, the account to use.

## Help from the office library
`page-builder` (pages and sections) and `design-polish` (spacing, type, states): find them with find_skills.
