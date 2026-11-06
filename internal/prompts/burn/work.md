[Burn] Work on piece [[.ID]] ([[.Kind]]): [[.Title]]
[[- if .Detail]]
Detail: [[.Detail]]
[[- end]]
[[- if ne .Kind "quest"]]
A Burn scan found this piece and checked it; the detail is your brief (where, evidence, fix, verify). Start from it: confirm it in the code, do not scan the codebase again.
[[- end]]
[[- if .Lessons]]
Lessons this Burn learned from earlier pieces (follow them):
[[.Lessons]]
[[- end]]
[[- if .Map]]
The scans keep a code map: burn_list(what="map") when you need to find your way.
[[- end]]
[[- if .Again]]
This piece is half done: look at git status / git diff in this worktree to see how far it got, then go on.
[[- end]]
[[- if .Fresh]]
This worktree starts over from the run's latest code: the changes of your earlier attempt are gone. Do the work again in full; do not rely on what this chat says was already done, check git diff.
[[- end]]
[[- if .Focus]]
The person's focus (lean towards it when there is a choice of how): [[.Focus]]
[[- end]]
[[- range .FocusChecks]]
Verify: [[.]].
[[- end]]
[[- if .ReviewNote]]
The reviewer's notes (follow them unless the code shows otherwise):
[[.ReviewNote]]
[[- end]]

You work in this piece's own worktree, with full access; other Burn pieces may run in parallel in theirs. Do not push or merge. After changing code, run the related build/test until they pass, in the foreground: never end your turn waiting on a background command (nothing wakes you when it ends).
[[- if eq .Kind "bug"]]
A bug fix comes with a regression test that FAILS without the fix: run it once against the code with your fix taken out (copy the fixed non-test files aside, `git checkout -- <those files>`, run the test, copy them back; not git stash, it is shared with other worktrees) and say in your summary that it failed before and passes now. A test that passes either way proves nothing; with no way to test it, say why.
[[- end]]
[[- if eq .Kind "quest"]]
The person gave this piece themselves: do what it asks, as asked. Do not look for other work; anything else you notice goes in your summary.
[[- end]]
[[- if eq .Kind "idea"]]
A new idea: build it. A large one: do its first part that runs on its own, record the design in the project's docs (spec/ADR) and what is left, for later turns.
[[- end]]
[[- if eq .Kind "unfinished"]]
If this is a part of a roadmap feature: do exactly this part's scope, record design decisions in the project's docs (spec/ADR) and mark the progress in the planning docs; later parts are for later turns.
[[- end]]
[[- if .Reviewed]]
Once you report done, the result is reviewed first; if the review fails, the piece comes back with the reviewer's notes.
[[- end]]
[[- if .Verify]]
Once you report done, office runs these checks in this worktree first; one failing, the piece comes back to you with its output, so run them yourself before:
[[- range .Verify]]
- `[[.]]`
[[- end]]
[[- end]]
Once you report done, office merges your changes into this Burn run's branch [[.Branch]] as one commit (every piece of the run goes there).
Before calling burn_done: run `git status`/`git diff` in this worktree and name at least one real file change (path + what changed) in your summary, unless the piece only asked you to investigate or plan — then say what you found instead. If code changes were expected and there are none, call burn_fail instead and say why.
Finish with burn_done(item=[[printf "%q" .ID]], summary=what you did and how you verified it) or burn_fail(item=[[printf "%q" .ID]], reason=…) if you cannot do it.
