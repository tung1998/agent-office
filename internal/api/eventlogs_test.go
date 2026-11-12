package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A console is followed on the page's one stream ("log" events), not an SSE
// of its own (ADR-158): a process's lines come in order, another page's
// stream is refused, and asking from a seen line goes on after it.
func TestEventsFollowLog(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/processes", map[string]any{
		"name": "say", "command": "echo một; echo hai; echo ba", "cwd": ".", "kind": "job",
	}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create process = %d %v", resp.StatusCode, body)
	}
	id := body["id"].(string)

	rctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", e.srv.URL+"/api/events", nil)
	res, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	name, sid := "", -1
	for sid < 0 && sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok && name == "hello" {
			var h struct{ SID int }
			json.Unmarshal([]byte(v), &h)
			sid = h.SID
		}
	}

	follow := func(s int, src, target string, from int) int {
		r, _ := do(t, admin, "POST", e.srv.URL+"/api/events/logs", map[string]any{"sid": s, "key": "c1", "source": src, "id": target, "from": from, "on": true}, nil)
		return r.StatusCode
	}
	if st := follow(sid+100, "process", id, 0); st != 404 {
		t.Fatalf("another page's stream = %d", st)
	}
	if st := follow(sid, "process", "nope", 0); st != 404 {
		t.Fatalf("unknown process = %d", st)
	}
	if st := follow(sid, "bogus", id, 0); st != 400 {
		t.Fatalf("bad source = %d", st)
	}
	if r, b := do(t, admin, "POST", e.srv.URL+"/api/processes/"+id+"/start", nil, nil); r.StatusCode != 200 {
		t.Fatalf("start = %d %v", r.StatusCode, b)
	}
	if st := follow(sid, "process", id, 0); st != 204 {
		t.Fatalf("follow = %d", st)
	}

	// the lines, in order, until "ba"
	var texts []string
	last := 0
	for sc.Scan() && !strings.Contains(strings.Join(texts, "|"), "ba") {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			name = v
		}
		v, ok := strings.CutPrefix(line, "data: ")
		if !ok || name != "log" {
			continue
		}
		var d struct {
			Key   string
			Lines []struct {
				Seq    int
				Text   string
				Stream string
			}
		}
		json.Unmarshal([]byte(v), &d)
		if d.Key != "c1" {
			t.Fatalf("key = %q", d.Key)
		}
		for _, l := range d.Lines {
			if l.Seq <= last {
				t.Fatalf("line %d after %d", l.Seq, last)
			}
			last = l.Seq
			if l.Stream == "out" {
				texts = append(texts, l.Text)
			}
		}
	}
	if got := strings.Join(texts, "|"); got != "một|hai|ba" {
		t.Fatalf("lines = %q", got)
	}

	// a bad ask for the same console is refused and does not stop it
	if r, _ := do(t, admin, "POST", e.srv.URL+"/api/events/logs", map[string]any{"sid": sid, "key": "c1", "source": "process", "id": "nope", "on": true}, nil); r.StatusCode != 404 {
		t.Fatalf("bad replacement = %d", r.StatusCode)
	}

	// asked from a line past the last one (office restarted, numbered again):
	// the lines come again from the start
	r2, _ := do(t, admin, "POST", e.srv.URL+"/api/events/logs", map[string]any{"sid": sid, "key": "c2", "source": "process", "id": id, "from": 9999, "on": true}, nil)
	if r2.StatusCode != 204 {
		t.Fatalf("follow from past the end = %d", r2.StatusCode)
	}
	again := ""
	for again == "" && sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			name = v
		}
		v, ok := strings.CutPrefix(line, "data: ")
		if !ok || name != "log" {
			continue
		}
		var d struct {
			Key   string
			Lines []struct{ Seq int }
		}
		json.Unmarshal([]byte(v), &d)
		if d.Key == "c2" && len(d.Lines) > 0 {
			again = fmt.Sprint(d.Lines[0].Seq)
		}
	}
	if again != "1" {
		t.Fatalf("first line again = %q, want 1", again)
	}

	// turned off: 204, and nothing more for it
	if r, _ := do(t, admin, "POST", e.srv.URL+"/api/events/logs", map[string]any{"sid": sid, "key": "c1", "on": false}, nil); r.StatusCode != 204 {
		t.Fatalf("off = %d", r.StatusCode)
	}
}
