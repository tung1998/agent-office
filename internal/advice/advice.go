// Package advice notices an agent stuck on the same failure (ADR-262): the
// same command failing the same way twice in one run. The chat engine then
// asks an agent of another vendor for a second opinion once, and hands its
// answer to the stuck agent with the failure.
//
// It only counts: it knows nothing of agents, models or storage, so the
// office tools (run_command), a workflow's gate and the CLI runners' own
// tool errors all count through one Tracker.
package advice

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Settings modes (usage.Settings.AutoAdvisor).
const (
	ModeOff        = "off"
	ModeRepeat     = "repeat"      // a failure repeated in a run
	ModeRepeatDone = "repeat_done" // and a review of the answer of such a run before it ends
)

// Triggers of an advice (storage.Advice.Trigger).
const (
	TriggerRepeat = "repeat"
	TriggerDone   = "done"
)

// MaxPerRun is how many advices one run asks for at most (the review before
// it ends not counted): past it a stuck agent is left to stop and ask.
const MaxPerRun = 2

// Repeats is the failure count that asks: the second time the same thing
// fails the same way.
const Repeats = 2

// runTTL: a run not heard from in that long is forgotten (a task's run or a
// step whose end the engine does not see).
const runTTL = 24 * time.Hour

var (
	reANSI   = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	reTmp    = regexp.MustCompile(`(/private)?(/var/folders|/tmp|/var/tmp)/[^\s:'"]*`)
	reHex    = regexp.MustCompile(`\b(0x)?[0-9a-f]{7,}\b`)
	reNum    = regexp.MustCompile(`\d+(\.\d+)?`)
	reSpace  = regexp.MustCompile(`\s+`)
	reTiming = regexp.MustCompile(`\(\d+(\.\d+)?m?s\)|\b\d+(\.\d+)?(ms|s|m)\b`)
)

// normal makes two runs of one failure read alike: no colours, temporary
// paths, hashes, numbers or timings; spaces folded; lower case.
func normal(s string) string {
	s = reANSI.ReplaceAllString(s, "")
	s = reTmp.ReplaceAllString(s, "<tmp>")
	s = strings.ToLower(s)
	s = reTiming.ReplaceAllString(s, "")
	s = reHex.ReplaceAllString(s, "<h>")
	s = reNum.ReplaceAllString(s, "#")
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

// Headline is the line of errText that says what failed: the first one that
// reads as an error (FAIL, error, panic…), else the first that is not empty.
func Headline(errText string) string {
	first := ""
	for _, l := range strings.Split(errText, "\n") {
		l = strings.TrimSpace(reANSI.ReplaceAllString(l, ""))
		if l == "" || strings.HasPrefix(l, "$ ") {
			continue
		}
		if first == "" {
			first = l
		}
		low := strings.ToLower(l)
		for _, w := range []string{"fail", "error", "panic", "cannot", "undefined", "not found", "no such", "denied", "exit status", "lỗi"} {
			if strings.Contains(low, w) {
				return clip(l, 300)
			}
		}
	}
	return clip(first, 300)
}

// Fingerprint is what makes two failures "the same": the command and the
// line saying what failed, both made normal.
func Fingerprint(command, errText string) string {
	return normal(command) + " ⟂ " + normal(Headline(errText))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// Failure is what Tracker.Failed found.
type Failure struct {
	Print string // Fingerprint
	Count int    // times it failed so in this run
	Ask   bool   // ask an advisor now: the second time, once per print, MaxPerRun at most
}

type run struct {
	seen    map[string]int
	command map[string]string // print → the command as first run
	asked   map[string]bool
	asks    int
	pending []string
	last    time.Time
}

// Tracker counts failures per run (job id). The zero value is ready.
type Tracker struct {
	mu   sync.Mutex
	runs map[string]*run
	// Now is the clock (tests); nil = time.Now.
	Now func() time.Time
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// get is the run of jobID, made if missing (t.mu held); it forgets runs
// past runTTL.
func (t *Tracker) get(jobID string) *run {
	now := t.now()
	if t.runs == nil {
		t.runs = map[string]*run{}
	}
	for id, r := range t.runs {
		if now.Sub(r.last) > runTTL {
			delete(t.runs, id)
		}
	}
	r := t.runs[jobID]
	if r == nil {
		r = &run{seen: map[string]int{}, command: map[string]string{}, asked: map[string]bool{}}
		t.runs[jobID] = r
	}
	r.last = now
	return r
}

// Failed counts one failure of command in run jobID. Ask is true the
// Repeats-th time it fails so, once per print, while the run has asked
// fewer than MaxPerRun times; it is then counted as asked. A run without
// a job id is not counted.
func (t *Tracker) Failed(jobID, command, errText string) Failure {
	f := Failure{Print: Fingerprint(command, errText)}
	if jobID == "" || strings.TrimSpace(command) == "" {
		return f
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.get(jobID)
	r.seen[f.Print]++
	if r.command[f.Print] == "" {
		r.command[f.Print] = clip(strings.TrimSpace(command), 300)
	}
	f.Count = r.seen[f.Print]
	if f.Count >= Repeats && !r.asked[f.Print] && r.asks < MaxPerRun {
		r.asked[f.Print], f.Ask = true, true
		r.asks++
	}
	return f
}

// Repeated: run jobID had a failure repeated (an advice asked or not).
func (t *Tracker) Repeated(jobID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[jobID]
	if r == nil {
		return false
	}
	for _, n := range r.seen {
		if n >= Repeats {
			return true
		}
	}
	return false
}

// Failures are the commands that failed again and again in run jobID.
func (t *Tracker) Failures(jobID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[jobID]
	if r == nil {
		return nil
	}
	var out []string
	for p, n := range r.seen {
		if n >= Repeats {
			out = append(out, r.command[p])
		}
	}
	sort.Strings(out)
	return out
}

// Asks is how many advices run jobID asked for.
func (t *Tracker) Asks(jobID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.runs[jobID]; r != nil {
		return r.asks
	}
	return 0
}

// Push keeps an advice for run jobID that could not go with the failure
// (the CLI's own tool failed): the run's next office tool call carries it.
func (t *Tracker) Push(jobID, note string) {
	if jobID == "" || strings.TrimSpace(note) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.get(jobID)
	r.pending = append(r.pending, note)
}

// Take hands over and forgets the advices kept for run jobID.
func (t *Tracker) Take(jobID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[jobID]
	if r == nil || len(r.pending) == 0 {
		return nil
	}
	out := r.pending
	r.pending = nil
	return out
}

// Drop forgets run jobID (it ended).
func (t *Tracker) Drop(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.runs, jobID)
}

// Runs is how many runs are counted (tests).
func (t *Tracker) Runs() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.runs)
}

// Objects reads a review before a run ends: its first line says OBJECT.
func Objects(answer string) bool {
	line, _, _ := strings.Cut(strings.TrimSpace(answer), "\n")
	line = strings.ToUpper(strings.Trim(strings.TrimSpace(line), "*_#:`> "))
	return strings.HasPrefix(line, "OBJECT")
}
