package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/perm"
)

// checkSays runs the hook on file (relative to dir) and returns what it said.
func checkSays(t *testing.T, dir, file string) string {
	t.Helper()
	in, _ := json.Marshal(map[string]any{"cwd": dir, "tool_name": "Edit", "tool_input": map[string]any{"file_path": file}})
	var out bytes.Buffer
	QuickCheck(bytes.NewReader(in), &out, nil)
	return out.String()
}

func goModule(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	return dir
}

// A Go file badly formatted, with a syntax error or a vet finding is sent
// back to the agent as a block; a good one passes.
func TestQuickCheckGo(t *testing.T) {
	t.Parallel()
	dir := goModule(t)
	write := func(name, src string) { os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644) }

	write("a.go", "package x\n\nfunc A() int { return 1 }\n")
	if s := checkSays(t, dir, "a.go"); s != "" {
		t.Fatalf("good file blocked: %s", s)
	}
	write("a.go", "package x\nfunc A()   int { return 1 }\n")
	s := checkSays(t, dir, "a.go")
	if !strings.Contains(s, `"decision":"block"`) || !strings.Contains(s, "gofmt -w a.go") {
		t.Fatalf("unformatted file: %s", s)
	}
	write("a.go", "package x\n\nfunc A() int { return\n")
	if s := checkSays(t, dir, "a.go"); !strings.Contains(s, `"block"`) || !strings.Contains(s, "a.go:") {
		t.Fatalf("syntax error: %s", s)
	}
	write("a.go", "package x\n\nimport \"fmt\"\n\nfunc A() { fmt.Printf(\"%d\", \"s\") }\n")
	if s := checkSays(t, dir, "a.go"); !strings.Contains(s, "a.go:5") {
		t.Fatalf("vet finding: %s", s)
	}
	write("a.go", "package x\n\nfunc A() int { return 1 }\n")
	write("b.go", "package x\n\nfunc B() int { return undefinedName }\n") // another file's trouble: not this edit's
	if s := checkSays(t, dir, "a.go"); s != "" {
		t.Fatalf("another file's error reported: %s", s)
	}
	if s := checkSays(t, dir, "b.go"); !strings.Contains(s, "undefinedName") {
		t.Fatalf("type error: %s", s)
	}
}

func TestQuickCheckDataFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, src string) { os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644) }
	write("ok.json", `{"a": [1, 2]}`)
	write("bad.json", "{\n  \"a\": 1,\n}\n")
	write("ok.yaml", "a: 1\n---\nb: [1, 2]\n")
	write("bad.yml", "a: [1, 2\n")
	write("x.ts", "const a: number = 'nope'\n")
	for _, f := range []string{"ok.json", "ok.yaml", "x.ts", "missing.go"} {
		if s := checkSays(t, dir, f); s != "" {
			t.Errorf("%s blocked: %s", f, s)
		}
	}
	if s := checkSays(t, dir, "bad.json"); !strings.Contains(s, "JSON sai ở dòng 3") {
		t.Errorf("bad.json: %s", s)
	}
	if s := checkSays(t, dir, "bad.yml"); !strings.Contains(s, "YAML sai") {
		t.Errorf("bad.yml: %s", s)
	}
}

// ADR-135: the project's own checks run by file ending, after the built-in
// ones, carried to the hook on its command line.
func TestQuickCheckOwn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a b.ts"), []byte("let x = 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "ok.md"), []byte("x\n"), 0o644)
	rules, err := perm.ParseQuickChecks("# own\n.ts .vue: grep -q 'const' {file} || { echo cần const; exit 1; }\n.md: true")
	if err != nil || len(rules) != 2 {
		t.Fatal(rules, err)
	}
	says := func(file string, rules []perm.QuickCheckRule) string {
		in, _ := json.Marshal(map[string]any{"cwd": dir, "tool_name": "Edit", "tool_input": map[string]any{"file_path": file}})
		var out bytes.Buffer
		QuickCheck(bytes.NewReader(in), &out, rules)
		return out.String()
	}
	if s := says("a b.ts", rules); !strings.Contains(s, "cần const") || !strings.Contains(s, `"decision":"block"`) {
		t.Fatalf("own check not reported: %s", s)
	}
	if s := says("ok.md", rules); s != "" {
		t.Fatalf("passing own check reported: %s", s)
	}
	if s := says("a b.ts", nil); s != "" {
		t.Fatalf("no own check, still reported: %s", s)
	}
	// on the command line and back
	set := checkSettings(".ts: eslint {file}")
	cmd := set["hooks"].([]map[string]any)[0]["command"].(string)
	enc := cmd[strings.Index(cmd, "--checks ")+len("--checks "):]
	if got := DecodeChecks(enc); len(got) != 1 || got[0].Command != "eslint {file}" || got[0].Exts[0] != ".ts" {
		t.Fatalf("decoded %+v from %q", got, cmd)
	}
	if _, err := perm.ParseQuickChecks("ts: x"); err == nil {
		t.Fatal("an ending without a dot")
	}
	if _, err := perm.ParseQuickChecks(".ts"); err == nil {
		t.Fatal("no command")
	}
}
