---
name: build
description: How to build a feature or fix a bug in this mobile app: read before writing, test first, the repo's patterns, every screen state, permissions, the checks and builds to run before handing back. Use whenever you change the app's code.
---

# Build

## Before writing
- `docs/stack.md`: the framework and the commands (install, start, typecheck, lint, test, iOS build, Android build, UI flows). Missing a command: find it in `package.json`, `pubspec.yaml`, `app.json`/`app.config.*`, Gradle or the Xcode project, and add it there.
- The spec and its acceptance criteria. Then the code around the change: the nearest screen, component, store, API client and their tests. Reuse before creating.
- A library or SDK API you are unsure of: read its current docs (context7 MCP when connected, else the official site). Expo and React Native change fast: check the version in the repo.

## While writing
- Test first: a unit or component test that fails, then passes. A bug: a test or a Maestro/Detox flow that reproduces it.
- Screens: loading, empty, error, offline and success states; safe areas; small (iPhone SE) and large phones; dark mode; large font / font scale; accessibility labels on touchables and images; no hard-coded text if the app has i18n.
- Platform differences: Android back button and gestures, keyboard covering inputs, status bar; use the platform's conventions, not one look forced on both.
- Permissions: asked at the moment they are needed, with a clear reason string (Info.plist / AndroidManifest / app config); denied handled with a way forward.
- Data: tokens in secure storage (Keychain/Keystore, expo-secure-store), never in plain AsyncStorage/SharedPreferences; HTTPS only; long lists virtualised; images sized.
- Small steps; one concern per change. No dead code.

## Secrets, keys and dependencies
- Secrets only in the env file that stays out of git; a new variable is named (without its value) in the example env file and `docs/stack.md`. Anything shipped in the app bundle is public: no secret key there.
- Signing keys, keystores, provisioning profiles and store credentials: never touched, never committed.
- A new dependency or native module: why, licence, size, whether it needs a native rebuild (Expo: a config plugin or a dev build); nothing paid without the owner's yes.

## Before handing back
Run, in order, and give each command with its result: install (if dependencies changed), typecheck, lint, tests, then a build for each platform this machine can build (iOS needs macOS with Xcode; say so when it cannot). Then say how to see the change: the screen, the steps, the account to use, the platform checked.

## Help from the office library
`design-polish` (spacing, type, states): find it with find_skills.
