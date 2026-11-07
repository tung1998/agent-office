package mcpgateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/ids"
)

// Long tool results (ADR-183): an agent gets the head and the tail of a text
// result longer than MaxResult, and a note to read the middle with the office
// tool mcp_result. Office keeps the whole text for a while, for the chat or
// run that called.
const (
	DefaultMaxResult = 30 << 10
	cutKeep          = time.Hour
	cutCap           = 64 << 20 // bytes of results kept in all
	// RestLimit is what one mcp_result call returns at most.
	RestLimit = 30 << 10
)

// callSize is what one call weighed, for the call log.
type callSize struct{ in, out, cut int64 }

type cutEntry struct {
	id, conv, job string
	text          string
	at            time.Time
}

// cuts keeps the whole text of results office cut.
type cuts struct {
	mu    sync.Mutex
	list  []*cutEntry // oldest first
	bytes int
}

func (s *cuts) add(e *cutEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, e)
	s.bytes += len(e.text)
	for len(s.list) > 0 && (s.bytes > cutCap || time.Since(s.list[0].at) > cutKeep) {
		s.bytes -= len(s.list[0].text)
		s.list = s.list[1:]
	}
}

func (s *cuts) get(id string) *cutEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if e.id == id && time.Since(e.at) <= cutKeep {
			return e
		}
	}
	return nil
}

func (g *Gateway) maxResult() int {
	if g.MaxResult == 0 {
		return DefaultMaxResult
	}
	return g.MaxResult
}

// cutText shortens text longer than the limit to its head and tail with a
// note; n is the bytes cut out. Only a run's call is cut: it can read the
// rest (a person's own CLI gets everything).
func (g *Gateway) cutText(c Caller, server, tool, text string) (string, int) {
	limit := g.maxResult()
	// the office assistant has no mcp_result (feature packs' tools are not its own)
	if limit < 0 || len(text) <= limit || c.Person() || c.AgentID == AssistantID || (c.ConversationID == "" && c.JobID == "") {
		return text, 0
	}
	head := lineEnd(text, limit*4/5)
	tail := lineStart(text, len(text)-limit/5)
	if tail <= head {
		return text, 0
	}
	e := &cutEntry{id: ids.New("mcr"), conv: c.ConversationID, job: c.JobID, text: text, at: time.Now()}
	g.cuts.add(e)
	note := fmt.Sprintf("\n\n[office: kết quả của %s/%s dài %d byte nên đã cắt %d byte ở giữa (byte %d–%d). Đọc phần bị cắt bằng tool mcp_result của office: id=%q, offset=%d.]\n\n",
		server, tool, len(text), tail-head, head, tail, e.id, head)
	return text[:head] + note + text[tail:], tail - head
}

// lineEnd is where a head of about n bytes ends: after a newline when one
// is in its last half, else on a rune boundary.
func lineEnd(s string, n int) int {
	if i := strings.LastIndexByte(s[:n], '\n'); i >= n/2 {
		return i + 1
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// lineStart is where a tail from about n starts: after the next newline
// when it is near, else on a rune boundary.
func lineStart(s string, n int) int {
	if i := strings.IndexByte(s[n:], '\n'); i >= 0 && i < (len(s)-n)/2 {
		return n + i + 1
	}
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return n
}

// cutResult shortens the text of a tools/call result. A result with
// structuredContent stays whole: a client may check it against the tool's
// outputSchema.
func (g *Gateway) cutResult(c Caller, server, tool string, result json.RawMessage) (json.RawMessage, int) {
	var res map[string]json.RawMessage
	if json.Unmarshal(result, &res) != nil {
		return result, 0
	}
	if sc := bytes.TrimSpace(res["structuredContent"]); len(sc) > 0 && string(sc) != "null" {
		return result, 0
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(res["content"], &content) != nil {
		return result, 0
	}
	var texts []string
	first := -1
	for i, part := range content {
		var typ, text string
		_ = json.Unmarshal(part["type"], &typ)
		if typ != "text" || json.Unmarshal(part["text"], &text) != nil {
			continue
		}
		if first < 0 {
			first = i
		}
		texts = append(texts, text)
	}
	if first < 0 {
		return result, 0
	}
	short, n := g.cutText(c, server, tool, strings.Join(texts, "\n"))
	if n == 0 {
		return result, 0
	}
	// All text parts were joined into one string above and cut as a whole;
	// short already holds the head and tail of that joined string, which
	// covers the first and last text parts. The other text parts are
	// dropped here on purpose, not lost: the full joined text (every part)
	// stays in the cutEntry short's note points to, readable in full via
	// mcp_result. Non-text parts (e.g. images) are kept unchanged.
	kept := []map[string]json.RawMessage{}
	for i, part := range content {
		var typ string
		_ = json.Unmarshal(part["type"], &typ)
		switch {
		case i == first:
			part["text"], _ = json.Marshal(short)
			kept = append(kept, part)
		case typ != "text":
			kept = append(kept, part)
		}
	}
	res["content"], _ = json.Marshal(kept)
	b, err := json.Marshal(res)
	if err != nil {
		return result, 0
	}
	return b, n
}

// cutMessage shortens the result in one JSON-RPC answer (a batch is left
// as it is).
func (g *Gateway) cutMessage(c Caller, server, tool string, msg []byte) ([]byte, int) {
	var fields map[string]json.RawMessage
	t := bytes.TrimSpace(msg)
	if len(t) == 0 || t[0] != '{' || json.Unmarshal(t, &fields) != nil || len(fields["result"]) == 0 {
		return msg, 0
	}
	res, n := g.cutResult(c, server, tool, fields["result"])
	if n == 0 {
		return msg, 0
	}
	fields["result"] = res
	b, err := json.Marshal(fields)
	if err != nil {
		return msg, 0
	}
	return b, n
}

// writeCut copies an HTTP server's answer to a tools/call, cutting a long
// result: a JSON body whole, an SSE stream event by event.
func (g *Gateway) writeCut(w http.ResponseWriter, body io.Reader, sse bool, c Caller, server, tool string) (out, cut int64) {
	if !sse {
		raw, _ := io.ReadAll(io.LimitReader(body, 256<<20))
		short, n := g.cutMessage(c, server, tool, raw)
		k, _ := w.Write(short)
		return int64(k), int64(n)
	}
	rc := http.NewResponseController(w)
	br := bufio.NewReaderSize(body, 32<<10)
	var event []string
	flush := func() bool {
		if len(event) == 0 {
			return true
		}
		var data []string
		var other []string
		for _, l := range event {
			if v, ok := strings.CutPrefix(l, "data:"); ok {
				data = append(data, strings.TrimPrefix(v, " "))
			} else {
				other = append(other, l)
			}
		}
		lines := event
		if len(data) > 0 {
			if short, n := g.cutMessage(c, server, tool, []byte(strings.Join(data, "\n"))); n > 0 {
				cut += int64(n)
				lines = append(other, "data: "+string(short))
			}
		}
		event = event[:0]
		k, err := io.WriteString(w, strings.Join(lines, "\n")+"\n\n")
		out += int64(k)
		_ = rc.Flush()
		return err == nil
	}
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			l := strings.TrimRight(line, "\r\n")
			if l == "" {
				if !flush() {
					return
				}
			} else {
				event = append(event, l)
			}
		}
		if err != nil {
			flush()
			return
		}
	}
}

// Rest reads part of a result office cut, for the chat (conv) or run (job)
// that called.
func (g *Gateway) Rest(id, conv, job string, offset, limit int) (string, error) {
	e := g.cuts.get(id)
	if e == nil || !((e.conv != "" && e.conv == conv) || (e.job != "" && e.job == job)) {
		return "", errors.New("office không còn giữ kết quả " + id + " (giữ 1 giờ, chỉ cho chat đã gọi): gọi lại tool, thu hẹp tham số")
	}
	if limit <= 0 || limit > RestLimit {
		limit = RestLimit
	}
	limit = max(limit, 256)
	if offset < 0 || offset >= len(e.text) {
		return "", fmt.Errorf("offset ngoài kết quả (dài %d byte)", len(e.text))
	}
	for offset > 0 && !utf8.RuneStart(e.text[offset]) {
		offset--
	}
	end := min(offset+limit, len(e.text))
	for end < len(e.text) && !utf8.RuneStart(e.text[end]) {
		end--
	}
	more := ""
	if end < len(e.text) {
		more = fmt.Sprintf(" Đọc tiếp: offset=%d.", end)
	}
	return fmt.Sprintf("[byte %d–%d / %d.%s]\n%s", offset, end, len(e.text), more, e.text[offset:end]), nil
}
