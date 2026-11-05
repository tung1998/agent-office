---
name: verify-app
description: How to check the mobile app on a device: run it on an iOS simulator and an Android emulator, walk each acceptance criterion (small and large screen, dark mode, large font, offline, permission denied, background), Maestro or Detox flows, screenshots as evidence. Use when reviewing a feature or a fix, reproducing a bug, or for the smoke test of a release.
---

# Verify on a device

## Tools (check what is there first)
- iOS simulator: `xcrun simctl list devices` (macOS with Xcode only). Android emulator: `emulator -list-avds`, `adb devices`.
- Run the app: the start command of `docs/stack.md` (Expo: `npx expo run:ios` / `run:android` or a development build; Flutter: `flutter run -d <device>`).
- Screenshots: `xcrun simctl io booted screenshot <file>`, `adb exec-out screencap -p > <file>`; save them under `docs/evidence/<date>-<slug>/`.
- UI flows: Maestro (`maestro test <flow.yaml>`) or Detox when the repo has them; a new flow for a new main path is welcome.
- No simulator or emulator: say so in the verdict. An Expo app with a web build: Playwright MCP on it covers the logic, not the native parts; say which. Ask the owner to install Xcode or Android Studio or connect a device; never install anything yourself.

## The walk
For each acceptance criterion, on each platform available:
1. A small phone (iPhone SE / a 5" Android) and a large one: do the steps, check the result. Screenshot.
2. Dark mode and the largest font size: nothing cut off or overlapping, text readable.
3. States: empty data, an error, offline (airplane mode / network link conditioner), slow loading.
4. Permissions: allow, then deny (reset with `xcrun simctl privacy` / `adb shell pm revoke`): the app explains and offers a way forward.
5. Lifecycle: send the app to the background and back, rotate if supported, kill and reopen: state kept where it should be.
6. Logs: no red box, no crash, no new warnings from the change (Metro / `adb logcat` / Xcode console).

## Accessibility quick check
Touch targets at least 44×44 pt / 48×48 dp, labels on icons and images (VoiceOver / TalkBack read them sensibly), contrast readable, focus order sensible.

## Evidence
A table: criterion, iOS, Android, small/large, dark/large font, result (pass/fail), the screenshot. Fail: steps to reproduce, device and OS, expected, actual. Then what you could not check and why (a platform missing is never "pass").
