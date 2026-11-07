package prompts

// A Trace is one recorded prompt call, fixed as a regression test: Name is
// the prompt ("area/name"), Data the value Render was called with (nil for a
// prompt that takes none). TestTraces re-renders every trace under
// testdata/traces/ and compares it to its golden file, so a wording change
// in internal/prompts/*.md that an agent actually saw is caught before
// merge instead of only noticed later as a behavior change. See
// testdata/traces/README.md for how to add one.
type Trace struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data,omitempty"`
}

// Output reproduces what office sent for this trace.
func (tr Trace) Output() string {
	return Render(tr.Name, tr.Data)
}
