package verify

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseRulesAndForDiff(t *testing.T) {
	rules := "# comment\ngo test ./...\ndashboard/ .vue .TS: pnpm --dir dashboard typecheck\necho a: b\n"
	list, err := ParseRules(rules)
	if err != nil || len(list) != 3 {
		t.Fatalf("rules = %+v, %v", list, err)
	}
	if r := list[1]; r.Command != "pnpm --dir dashboard typecheck" || !slices.Equal(r.Dirs, []string{"dashboard/"}) || !slices.Equal(r.Ends, []string{".vue", ".ts"}) {
		t.Fatalf("scoped rule = %+v", r)
	}
	if r := list[2]; r.Command != "echo a: b" || len(r.Ends)+len(r.Dirs) != 0 {
		t.Fatalf("a command with \": \" is not a scope: %+v", r)
	}
	if got := ForDiff(rules, "", []string{"internal/a.go"}); !slices.Equal(got, []string{"go test ./...", "echo a: b"}) {
		t.Fatalf("Go diff runs = %v", got)
	}
	if got := ForDiff(rules, "", []string{"dashboard/app/x.vue"}); len(got) != 3 {
		t.Fatalf("dashboard diff runs = %v", got)
	}
	if _, err := ParseRules(strings.Repeat("x", maxRules+1)); err == nil {
		t.Fatal("too long is refused")
	}
}

// No merge checks set: the build checks the files suggest, only when the
// diff touches that language.
func TestForDiffGuesses(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	if got := ForDiff("", dir, []string{"a/b.go"}); !slices.Equal(got, []string{"go build ./...", "go vet ./..."}) {
		t.Fatalf("Go diff = %v", got)
	}
	if got := ForDiff("", dir, []string{"docs/x.md"}); len(got) != 0 {
		t.Fatalf("docs diff builds nothing, got %v", got)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	if out := Run(context.Background(), []string{"true", "echo boom; exit 3"}, dir); !strings.Contains(out, "echo boom; exit 3") || !strings.Contains(out, "boom") {
		t.Fatalf("failed check = %q", out)
	}
	if out := Run(context.Background(), []string{"true"}, dir); out != "" {
		t.Fatalf("passing = %q", out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out := Run(ctx, []string{"false"}, dir); out != "" {
		t.Fatalf("stopped: not the work's fault, got %q", out)
	}
}
