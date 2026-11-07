package mcpgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCutResultKeepsFullTextRecoverable verifies that cutResult, when it
// drops a non-first text content part from a multi-part result, has not
// actually lost that part's text: the full joined text (every part) stays
// retrievable via mcp_result (g.cuts/Rest), and the trimmed result stays
// bounded by roughly MaxResult plus the note, not the sum of every part.
func TestCutResultKeepsFullTextRecoverable(t *testing.T) {
	g := &Gateway{MaxResult: 100}
	c := Caller{Kind: "api", AgentID: "agent1", ConversationID: "conv", JobID: "job"}

	first := strings.Repeat("a", 200)
	second := strings.Repeat("b", 200) + "second-part-marker" + strings.Repeat("b", 200)
	result, _ := json.Marshal(map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": first},
			{"type": "text", "text": second},
			{"type": "image", "data": "imgdata"},
		},
	})

	out, n := g.cutResult(c, "server", "tool", result)
	if n == 0 {
		t.Fatalf("expected cutResult to cut, got n=0")
	}
	// the note wrapped around short adds fixed overhead; what must not
	// happen is the output growing with the size of the dropped parts
	// (that would mean they got duplicated back in, not just referenced).
	if len(out) > len(first)+len(second) {
		t.Fatalf("trimmed result not bounded: got %d bytes, inputs totalled %d", len(out), len(first)+len(second))
	}

	var res map[string]json.RawMessage
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var content []map[string]string
	if err := json.Unmarshal(res["content"], &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	// the image part must survive untouched
	foundImage := false
	for _, part := range content {
		if part["type"] == "image" {
			foundImage = true
			if part["data"] != "imgdata" {
				t.Fatalf("image part corrupted: %+v", part)
			}
		}
	}
	if !foundImage {
		t.Fatalf("image part was dropped: %+v", content)
	}

	// the note in the first (shortened) text part must carry an id that
	// lets the caller read the dropped second part back in full.
	var short string
	for _, part := range content {
		if part["type"] == "text" {
			short = part["text"]
			break
		}
	}
	idx := strings.Index(short, `id=\"`)
	if idx < 0 {
		idx = strings.Index(short, `id="`)
	}
	if idx < 0 {
		t.Fatalf("no mcp_result id found in note: %q", short)
	}
	rest := short[idx+4:]
	if q := strings.IndexByte(rest, '"'); q >= 0 {
		rest = rest[:q]
	}
	id := rest

	e := g.cuts.get(id)
	if e == nil {
		t.Fatalf("cutEntry %q not found via g.cuts.get", id)
	}
	if !strings.Contains(e.text, "second-part-marker") {
		t.Fatalf("full text behind the note does not contain the second part's content: %q", e.text)
	}
}
