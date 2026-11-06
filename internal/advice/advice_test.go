package advice

import (
	"testing"
	"time"
)

func TestSameFailureTwiceTriggers(t *testing.T) {
	var tr Tracker
	a := tr.Failed("job1", "go test ./internal/chat", "--- FAIL: TestX (0.31s)\n    x_test.go:12: got 3 want 4\nFAIL\tpkg\t0.512s")
	if a.Ask || a.Count != 1 {
		t.Fatalf("first failure asked: %+v", a)
	}
	// the same failure, other timings, temp dir and numbers
	b := tr.Failed("job1", "go  test ./internal/chat", "--- FAIL: TestX (1.02s)\n    x_test.go:12: got 5 want 4\nFAIL\tpkg\t1.9s")
	if !b.Ask || b.Count != 2 || b.Print != a.Print {
		t.Fatalf("second same failure did not ask: %+v (first %q)", b, a.Print)
	}
	c := tr.Failed("job1", "go test ./internal/chat", "--- FAIL: TestX (2s)")
	if c.Ask {
		t.Fatal("a print is asked about once")
	}
	if !tr.Repeated("job1") || tr.Asks("job1") != 1 {
		t.Fatal("run not marked repeated")
	}
}

func TestDifferentFailuresDoNotTrigger(t *testing.T) {
	var tr Tracker
	if f := tr.Failed("j", "go build ./...", "x.go:3: undefined: foo"); f.Ask {
		t.Fatal(f)
	}
	if f := tr.Failed("j", "go build ./...", "y.go:9: syntax error: unexpected }"); f.Ask {
		t.Fatal("another error of the same command is not a repeat")
	}
	if f := tr.Failed("j", "go vet ./...", "x.go:3: undefined: foo"); f.Ask {
		t.Fatal("another command is not a repeat")
	}
	if f := tr.Failed("other", "go build ./...", "x.go:3: undefined: foo"); f.Ask {
		t.Fatal("another run counts apart")
	}
	if tr.Repeated("j") {
		t.Fatal("no repeat in j")
	}
	if f := tr.Failed("", "go build", "boom"); f.Count != 0 {
		t.Fatal("a run without job id is not counted")
	}
}

func TestAdviceCapPerRunTracker(t *testing.T) {
	var tr Tracker
	asked := 0
	for _, cmd := range []string{"a", "b", "c"} {
		tr.Failed("j", cmd, "boom")
		if tr.Failed("j", cmd, "boom").Ask {
			asked++
		}
	}
	if asked != MaxPerRun {
		t.Fatalf("asked %d, want %d", asked, MaxPerRun)
	}
}

func TestCounterDroppedWhenJobEnds(t *testing.T) {
	var tr Tracker
	tr.Failed("j", "make", "boom")
	tr.Push("j", "note")
	tr.Drop("j")
	if tr.Runs() != 0 || tr.Repeated("j") || tr.Take("j") != nil {
		t.Fatal("run kept after Drop")
	}
	if f := tr.Failed("j", "make", "boom"); f.Count != 1 {
		t.Fatal("count did not start over")
	}
	// a run never ended is forgotten after a day
	now := time.Now()
	tr.Now = func() time.Time { return now.Add(25 * time.Hour) }
	tr.Failed("k", "x", "y")
	if tr.Runs() != 1 {
		t.Fatalf("stale run kept: %d", tr.Runs())
	}
}

func TestPushTake(t *testing.T) {
	var tr Tracker
	tr.Push("j", "one")
	tr.Push("j", "two")
	if got := tr.Take("j"); len(got) != 2 || got[0] != "one" {
		t.Fatal(got)
	}
	if tr.Take("j") != nil {
		t.Fatal("taken twice")
	}
}

func TestHeadline(t *testing.T) {
	cases := map[string]string{
		"$ go test\nok a\n--- FAIL: TestY\nmore":    "--- FAIL: TestY",
		"\n\n  something went off  \nnext":          "something went off",
		"compiling\n./x.go:1: undefined: z\nexit 2": "./x.go:1: undefined: z",
	}
	for in, want := range cases {
		if got := Headline(in); got != want {
			t.Errorf("Headline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObjects(t *testing.T) {
	for in, want := range map[string]bool{
		"OBJECT\nthe test still fails": true,
		"**OBJECT:** no proof":         true,
		"OK\nlooks fine":               false,
		"I object":                     false,
		"":                             false,
	} {
		if Objects(in) != want {
			t.Errorf("Objects(%q) != %v", in, want)
		}
	}
}
