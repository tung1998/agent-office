---
name: spec
description: How to write a feature spec for the mobile app (problem, screens and flow, acceptance criteria per platform, permissions, offline, out of scope, data and API changes, risks) and how to propose a stack for a new app. Use before building anything bigger than an hour of work, when the owner asks for a new feature, or when the repo has no code yet.
---

# Spec

## A feature spec
Write `docs/specs/<YYYY-MM-DD>-<slug>.md` with these sections, short:

1. **Problem**: who has it, what they cannot do today, why it matters (one paragraph).
2. **Screens and flow**: the screens, the navigation between them (stack, tab, modal, deep link), the steps the user takes.
3. **Acceptance criteria**: a numbered list, each checkable by a test or on a simulator/emulator: "Given …, when …, then …". Cover the states: loading, empty, error, offline, permission denied, success; small and large phones; iOS and Android when they differ (back button, gestures, system dialogs).
4. **Permissions and privacy**: which permission, when it is asked, the reason string shown, what happens when denied; data collected and where it goes (for the privacy labels / data safety form).
5. **Out of scope**: what this spec does not do.
6. **Data and API**: new or changed endpoints, local storage, sync and caching; what happens to data of the previous app version (migrations of local storage).
7. **Files**: the screens, components, stores, API clients and tests it touches (read the code first, name real paths).
8. **Risks**: security (tokens, secure storage), performance (long lists, images, startup time), store review (guidelines that may reject it), what is hard to undo.
9. **Open questions**: what only the owner can answer. Never guess these.

Keep it to what the feature needs now. Record it in `features` (`spec`, `acceptance`, `platforms`, `status` `spec`).

## A new app: proposing a stack
- Ask the owner: what the app does, for whom, iOS, Android or both, does it need native features (camera, Bluetooth, background location, payments), offline use, the backend, the budget, what they already know, and whether they have a Mac (iOS builds need macOS and Xcode).
- Propose the simplest stack that fits, with one alternative and the trade-off. Defaults when nothing argues otherwise: Expo (React Native, TypeScript) with Expo Router, Jest + React Native Testing Library, Maestro for UI flows; Flutter when the team knows Dart; native Swift/Kotlin only for heavy platform features.
- Write it in `docs/stack.md` (the commands too) and the choice in `decisions` after the owner's yes.

## Help from the office library
`research-brief` (research on users or competitors' apps), `design-polish` (spacing, type, states): find them with find_skills, read one before using it.
