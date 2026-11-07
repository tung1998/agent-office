package ops

import (
	"io"
	"testing"
	"time"
)

// TestScanComposeLogsTooLongLine verifies that a line exceeding the scanner's
// max buffer surfaces an error notice and unblocks the writer instead of
// hanging forever (the writer simulates docker blocked on a full pipe).
func TestScanComposeLogsTooLongLine(t *testing.T) {
	pr, pw := io.Pipe()
	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	done := make(chan error, 1)
	go func() { done <- scanComposeLogs(pr, emit) }()

	writeErr := make(chan error, 1)
	go func() {
		defer pw.Close()
		if _, err := pw.Write([]byte("ok line\n")); err != nil {
			writeErr <- err
			return
		}
		// A single line over the 1<<20 scanner buffer limit.
		big := make([]byte, 2<<20)
		for i := range big {
			big[i] = 'x'
		}
		big[len(big)-1] = '\n'
		_, err := pw.Write(big)
		writeErr <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected scanComposeLogs to return the scanner error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scanComposeLogs hung instead of surfacing the scan error (regression)")
	}

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("expected the blocked writer to be unblocked with an error after the reader gave up")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer stayed blocked on the pipe instead of being unblocked (regression)")
	}

	if len(lines) == 0 || lines[0] != "ok line" {
		t.Fatalf("expected first good line to be emitted, got %+v", lines)
	}
	last := lines[len(lines)-1]
	if last == "ok line" {
		t.Fatal("expected an error notice to be emitted after the scanner stopped")
	}
}

func TestParsePS(t *testing.T) {
	lines := []byte(`{"ID":"abc","Name":"shop-redis-1","Service":"redis","State":"running","Status":"Up 2 minutes (healthy)","Health":"healthy","Publishers":[{"URL":"0.0.0.0","TargetPort":6379,"PublishedPort":6379,"Protocol":"tcp"},{"URL":"::","TargetPort":6379,"PublishedPort":6379,"Protocol":"tcp"}]}
{"ID":"def","Name":"shop-app-1","Service":"app","State":"exited","Status":"Exited (1) 3 seconds ago","Publishers":[]}`)
	rows := parsePS(lines)
	if len(rows) != 2 || rows[0].Service != "redis" || rows[1].State != "exited" {
		t.Fatalf("%+v", rows)
	}
	arr := parsePS([]byte(`[{"ID":"x","Service":"web","State":"running"}]`))
	if len(arr) != 1 || arr[0].Service != "web" {
		t.Fatalf("%+v", arr)
	}
	if len(parsePS(nil)) != 0 {
		t.Fatal("empty")
	}
}
