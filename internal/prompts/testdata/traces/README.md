# Trace fixtures

Each `<name>.json` here is one `prompts.Trace`: the prompt's name
(`"area/name"`, no `.md`) and the data it was rendered with. `TestTraces`
(`../../trace_test.go`) renders it and diffs the result against
`<name>.golden.txt`.

Add one when a prompt an agent actually saw matters enough to protect from a
silent wording regression (it changed behavior before, it is long and easy to
break with an edit, …) — not for every prompt file; `TestEveryPromptParses`
already catches a template that fails to parse or execute.

To add a trace:

1. Write `<name>.json` with the prompt's name and the data fields its
   template uses (check the `.Render("area/name", ...)` call site in Go for
   the field names; a trace's `data` can be a plain map even when the real
   caller uses a struct — `text/template` reads both the same way).
2. Generate its golden file: `OFFICE_UPDATE_TRACES=1 go test ./internal/prompts/...`
3. Read the new `<name>.golden.txt` and confirm it says what you expect
   before committing it — the golden file is the thing under test, not
   generated-and-trusted.

When a prompt's wording changes on purpose, re-run step 2 for the traces it
touches and read the diff before committing the new golden file.
