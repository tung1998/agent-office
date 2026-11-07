package prompts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTraces renders every fixture in testdata/traces and compares it to its
// golden .golden.txt, failing with a diff when a prompt change alters text
// a trace covers. Run with OFFICE_UPDATE_TRACES=1 to (re)write the golden
// files after checking the new wording by hand.
func TestTraces(t *testing.T) {
	const dir = "testdata/traces"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	update := os.Getenv("OFFICE_UPDATE_TRACES") == "1"
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		found = true
		base := strings.TrimSuffix(e.Name(), ".json")
		t.Run(base, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var tr Trace
			if err := json.Unmarshal(raw, &tr); err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			got := tr.Output()
			goldenPath := filepath.Join(dir, base+".golden.txt")
			if update {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("no golden for %s (run with OFFICE_UPDATE_TRACES=1 first): %v", base, err)
			}
			if got != string(want) {
				t.Errorf("%s: prompt text changed\n--- golden ---\n%s\n--- rendered ---\n%s", base, want, got)
			}
		})
	}
	if !found {
		t.Fatal("no trace fixtures in testdata/traces")
	}
}
