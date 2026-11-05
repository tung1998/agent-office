---
name: release
description: The checks before a store build of the mobile app (version and build numbers, clean builds, tests, dependency audit, device smoke test, permissions, privacy labels / data safety, store assets) and how to record the result and hand the upload to the owner. Use for the release check, the weekly dependency audit, or when the owner asks if the app is ready for TestFlight or Google Play.
---

# Release check

Each check with the command or tool and its result. A check that cannot run: say why; it is a warning, not a pass.

| # | Check | How | Blocks the release when |
|---|---|---|---|
| 1 | Version and build | version (semver) and build number / versionCode bumped above the last `releases` record and the store | not bumped |
| 2 | Clean builds | a fresh install, then the release build of each platform (`docs/stack.md`; EAS local build, `flutter build ipa/appbundle`, Gradle `bundleRelease`) without signing with the owner's keys | it fails |
| 3 | Tests | unit, component and UI flows (Maestro/Detox) | any fails |
| 4 | Typecheck and lint | the commands of `docs/stack.md` | errors |
| 5 | Dependency audit | `npm audit` / `pnpm audit`, `flutter pub outdated`, Gradle/CocoaPods outdated | a high or critical issue in a part the app uses |
| 6 | Smoke test | the main flows of `docs/product.md` on an iOS simulator and an Android emulator (skill `verify-app`) | a main flow breaks or crashes |
| 7 | Permissions | every permission in Info.plist / AndroidManifest / app config is used, with a clear reason string | one unused or without a reason |
| 8 | Privacy | the App Store privacy labels and the Play data safety form (`docs/store.md`) match what the code and its SDKs collect; a privacy policy URL | they do not match |
| 9 | Secrets | no secret in the diff since the last release and none in the app bundle (`git diff`, a search of the config) | a secret is committed or shipped |
| 10 | Store assets | `docs/store.md`: name, subtitle, description, keywords, screenshots per required size, icon, release notes | missing for a production release (warning for TestFlight / internal) |
| 11 | Store rules | target SDK level (Android), minimum Xcode / iOS SDK, guidelines the feature may touch (payments, login, user content), with sources checked on the web | a known rejection reason |

## Verdict and record
- PASS only when no check blocks. First line PASS or FAIL, then blockers, then warnings.
- Write the `releases` record (`version`, `build`, `date`, `platforms`, `notes`, `check`, `report`, `track` `none`); each blocker becomes a `bugs` record.
- A PASS is not an upload. Give the owner their checklist: build and sign with their own keys (or EAS with their account), upload to TestFlight / Play internal testing, paste the release notes, submit for review. Agents never sign, upload or submit; when the owner says it is up, set `track`.

## Help from the office library
`launch-checklist` (the wider launch: landing page, analytics, support email, backups): find it with find_skills.
