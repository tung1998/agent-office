package officetools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// deps_install takes the requirements files a pip install runs (ADR-251):
// the card installs those, says so, and a file outside the package is
// refused when the tool is called.
func TestDepsInstallToolFiles(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "ml"), 0o755)
	os.WriteFile(filepath.Join(root, "ml", "requirements.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(root, "ml", "requirements-dev.txt"), []byte("y\n"), 0o644)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "ml", Path: root})
	tb := New(st, nil, actions.New(st, nil))
	sc := Scope{ProjectID: p.ID, RunRef: "r1", Agent: "a", Level: perm.Propose}

	var schema string
	for _, tool := range tb.Tools() {
		if tool.Name == "deps_install" {
			b, _ := json.Marshal(tool.Schema)
			schema = string(b)
		}
	}
	if !strings.Contains(schema, `"files"`) {
		t.Fatalf("deps_install has no files: %s", schema)
	}

	out, isErr := tb.Call(ctx, sc, "deps_install", []byte(`{"path":"ml","files":["requirements-dev.txt"],"reason":"pytest 9"}`))
	if isErr || !strings.Contains(out, "requirements-dev.txt") {
		t.Fatalf("deps_install = %v %s", isErr, out)
	}
	acts, _ := st.Actions().List(ctx, "", "", "r1")
	if len(acts) != 1 || acts[0].Args.Deps == nil || len(acts[0].Args.Deps.Files) != 1 || acts[0].Args.Deps.Files[0] != "requirements-dev.txt" || acts[0].Args.Deps.Manager != "pip" {
		t.Fatalf("card = %+v", acts)
	}
	sc.RunRef = "r2"
	if out, isErr := tb.Call(ctx, sc, "deps_install", []byte(`{"path":"ml","files":["../../etc/passwd"],"reason":"x"}`)); !isErr {
		t.Fatalf("a file outside the package was taken: %s", out)
	}
}
