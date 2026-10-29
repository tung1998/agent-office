---
name: publish-public
description: Use when the person asks to push "lên 2 bên", "sang repo kia", or to publish commits to the public repo (tung1998/agent-office) as well as the private one.
---

# Push to both repos

Private repo: this project (`origin`). Public repo: `../agent-office`, its own history, fed only by scripts.

1. **Private is committed.** `git -C <project> status --short` holds nothing of this chat; `git -C <project> log --oneline origin/main..main` lists what the push will carry. Commits of other chats on main go too: name them in the report.
2. **Find what the public repo lacks.** Compare `git -C ../agent-office log --format=%s -30` with `git -C <project> log --format='%h %s' -30`; pick the private commits whose subject is not there yet. A commit touching only excluded paths (`docs/DECISIONS.md`, `docs/PLAN.md`, `docs/CLI.md`, `docs/superpowers`, `scripts/{export-public,public-sync,public-pick}.mjs`, see `scripts/public-pick.mjs:47`) never goes public: skip it.
3. **Public: cherry-pick, the default.** `node scripts/public-pick.mjs <commit>... --push` (or a range `a..b`). It strips internal notes, rewrites the module path and author, checks for leaks, applies with `git am -3`, then builds and vets; a failed step leaves the public repo as it was. Use `scripts/public-sync.mjs` (rewrites the public tree, `--push` without `--new` force-pushes) only when the person asks to rebuild the public repo.
4. **Private: propose `git_push`** with propose_action. It always waits for the person's card; never push private any other way.
5. **Report:** public commits pushed (`old..new`), checks the script ran, private push card waiting or done. Done only when `git -C ../agent-office log -1` shows the last pick and `git -C <project> log --oneline origin/main..main` is empty after the card.

A pick failing on a patch: read the script's error, fix the cause (not by hand-editing the public repo), and say what changed in the script; it is a commit of its own on private only.
