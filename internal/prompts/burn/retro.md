[Burn] This run of the Burn has ended (template: [[.Template]], retro prompt version [[.PromptVersion]]). Look back at it as the one who runs the process: not to judge the pieces one by one, but to find what kept the run from moving the project forward, so the next run is set up better. You are read only now: do not change code or settings.
[[- if .Focus]]

The person's focus: [[.Focus]]
[[- end]]

The run's numbers:
[[.Stats]]
[[- if .Setbacks]]

Pieces lately turned down or failed, and why:
[[- range .Setbacks]]
- [[.]]
[[- end]]
[[- end]]
[[- if .Lessons]]

The lessons the Burn keeps now:
[[.Lessons]]
[[- end]]

Read what you need (burn_list closed/open, the code, git log of the run's branch) to check the numbers against what happened, then answer in the person's language, short, with evidence:
1. What went wrong: the failures, the pieces turned down or done again and again, and their real cause (the process, the setup, the environment, the scans' judgment), not their symptoms.
2. What is missing: what the run did not reach (areas of the coverage plan, the project's goals and main flows, kinds of work it never recorded) and whether what it did was worth its cost.
3. What to change, most useful first: for the person (the Burn's settings: template, focus, review profile, checks, parallel pieces; or something in the project), and for Burn itself in office when the fault is its own (say which part: scanning, merging, reviews, checks).
Then end with the Burn's whole lessons list, updated (rules a scanner or worker can follow, the ones that no longer hold dropped, never a rule to skip areas unchanged since an earlier scan or run, at most about 3000 characters), in a block of its own:
```lessons
...
```
