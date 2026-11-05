---
name: release
description: The checks before a deploy or a demo of the web app (clean build, tests, typecheck and lint, dependency audit, browser smoke test, accessibility, performance) and how to record the result. Use for the release check, the weekly dependency audit, or when the owner asks if the app is ready.
---

# Release check

Each check with the command or tool and its result. A check that cannot run: say why; it is a warning, not a pass.

| # | Check | How | Blocks the release when |
|---|---|---|---|
| 1 | Clean build | a fresh install, then the build of `docs/stack.md` | it fails |
| 2 | Tests | unit and integration tests; e2e tests if the repo has them | any fails |
| 3 | Typecheck and lint | the commands of `docs/stack.md` | errors (warnings listed) |
| 4 | Dependency audit | `npm audit` / `pnpm audit` / `yarn npm audit`, `pip-audit`, `govulncheck`… | a high or critical issue in a part the app uses |
| 5 | Smoke test | the main flows of `docs/product.md` in the browser (skill `verify-ui`), desktop and 390px mobile | a main flow breaks |
| 6 | Console and network | during the smoke test | errors from the app |
| 7 | Accessibility | Lighthouse or axe, the quick check of `verify-ui` | a form or the main flow cannot be used by keyboard |
| 8 | Performance | Lighthouse (mobile) on the main page; bundle size against the last release | a large regression (say how large) |
| 9 | Secrets and config | no secret in the diff since the last release (`git diff`), every env variable named in `docs/stack.md` | a secret is committed |
| 10 | Migrations | run on a copy of the data, reversible or backed up | data would be lost |

## Verdict and record
- PASS only when no check blocks. First line PASS or FAIL, then blockers, then warnings.
- Write the `releases` record (`version`, `date`, `notes`, `check`, `report`); each blocker becomes a `bugs` record.
- A PASS is not a deploy: the deploy waits for the owner's yes.

## Help from the office library
`launch-checklist` (the wider launch: SEO, analytics, legal pages, backups): find it with find_skills.
