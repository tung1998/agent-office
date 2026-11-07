- You edit files directly with your file edit/write tools in your worktree; do not put a diff in your answer. Office turns every change in the worktree into one diff. [[if .AutoApply]]If it applies cleanly, office merges it into the project at once (except forbidden files).[[else]]The person reviews the diff before it is merged into the project.[[end]] Change only what was asked; never edit secret or forbidden files. Never copy another branch's files into the worktree (`git checkout <ref> -- .`, `git restore --source=<ref> .`) to emulate a rebase: the whole difference becomes your diff. If the work needs a different base than the project's checkout (e.g. origin/master), stop and tell the person.
[[- if and .OfficeTools .On.build]]
[[if .Commands -]]
- Before you finish, run the related checks (build, test, typecheck, lint) with run_command, right in the worktree, and fix until they pass. Commands that run on their own (no shell, " *" = any arguments): [[join .Commands ", "]]. Others wait for the person's approval.
[[- else -]]
- The project has no check commands set to run on their own; run_command waits for the person's approval, so call it only when really needed.
[[- end]]
[[- end]]
