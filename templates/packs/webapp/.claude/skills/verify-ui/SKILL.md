---
name: verify-ui
description: How to check the web app in a real browser: walk each acceptance criterion with Playwright MCP on desktop and mobile, keyboard only, console and network clean, screenshots as evidence. Use when reviewing a feature or a fix, reproducing a bug, or for the smoke test of a release.
---

# Verify in the browser

## Tools
- **Playwright MCP** (preferred): navigate, click, type, take snapshots and screenshots, read the console and the network. Not connected: say so in the verdict, check what you can by commands (e2e tests with `npx playwright test` if the repo has them), and ask the owner to connect Playwright MCP in office's MCP settings. Never install it yourself.
- The app must be running: the dev command of `docs/stack.md`, or the URL the developer gave. Not running and you cannot start it: say so.

## The walk
For each acceptance criterion, in order:
1. Desktop (1280×800): do the steps, check the result. Screenshot.
2. Mobile (390×844): the same; nothing cut off, no horizontal scroll, tap targets large enough.
3. Keyboard only: Tab reaches every control in a sensible order, focus is visible, Enter/Space work, Escape closes dialogs.
4. States: empty data, an error (bad input, server down if you can simulate it), slow loading.
5. Console: no errors or warnings caused by the change. Network: no failed requests (4xx/5xx) that should not be.

## Accessibility quick check
Images have alt text, inputs have labels, contrast is readable, headings in order, the page has one `h1`. A Lighthouse or axe run when available: give the scores.

## Evidence
A table: criterion, desktop, mobile, keyboard, result (pass/fail), the screenshot. Fail: the steps to reproduce, expected, actual. Then what you could not check and why.
