package collections

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-233: a file sent with a message lands in the project's
// .office-files/tam, kept out of git, and listed; keeping it moves it to
// giu; a file an agent made is copied there; cleaning takes only the
// temporary ones.
func TestFiles(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("# local\n*.log"), 0o644)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: root})
	s := New(st)

	src := filepath.Join(t.TempDir(), "hợp đồng.pdf")
	os.WriteFile(src, []byte("pdf"), 0o644)
	rel, err := s.SaveFile(ctx, p.ID, src, "../hợp đồng.pdf", "cnv_1", "human:a@x.io")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, ".office-files/tam/") || !strings.HasSuffix(rel, "-hợp đồng.pdf") {
		t.Fatalf("rel = %q", rel)
	}
	other, _ := s.SaveFile(ctx, p.ID, src, "log.txt", "cnv_1", "human:a@x.io")
	ex, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if string(ex) != "# local\n*.log\n/.office-files/\n" {
		t.Fatalf("exclude = %q", ex)
	}
	if list, _ := s.List(ctx, p.ID); len(list) != 2 { // links and files
		t.Fatalf("collections = %d", len(list))
	}

	x, err := s.KeepFile(ctx, p.ID, "hợp đồng.pdf", "hợp đồng ký 28/10", "agent:Dev") // by its name
	if err != nil {
		t.Fatal(err)
	}
	kept := x.Data["path"].(string)
	if x.Data["status"] != FileKept || !strings.HasPrefix(kept, ".office-files/giu/") || x.Data["note"] != "hợp đồng ký 28/10" {
		t.Fatalf("kept = %+v", x.Data)
	}
	if _, err := os.Stat(filepath.Join(root, rel)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the temporary copy stayed")
	}
	if b, _ := os.ReadFile(filepath.Join(root, kept)); string(b) != "pdf" {
		t.Fatalf("kept file = %q", b)
	}

	report := filepath.Join(root, "bao-cao.md") // made by an agent in the project
	os.WriteFile(report, []byte("# báo cáo"), 0o644)
	if x, err = s.KeepFile(ctx, p.ID, "bao-cao.md", "báo cáo tuần", "agent:Dev"); err != nil || x.Data["path"] != ".office-files/giu/bao-cao.md" {
		t.Fatalf("report = %+v, %v", x.Data, err)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatal("the agent's own file was moved, not copied")
	}
	if _, err := s.KeepFile(ctx, p.ID, "nope.pdf", "", "agent:Dev"); !errors.Is(err, ErrNoFile) {
		t.Fatalf("missing = %v", err)
	}

	if size, n := TempFiles(root); n != 1 || size != 3 {
		t.Fatalf("temp = %d, %d", size, n)
	}
	freed, n, err := s.CleanTemp(ctx, p.ID, "human:a@x.io")
	if err != nil || n != 1 || freed != 3 {
		t.Fatalf("clean = %d, %d, %v", freed, n, err)
	}
	if _, err := os.Stat(filepath.Join(root, other)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temp file stayed")
	}
	left, total, _ := s.Query(ctx, p.ID, FilesKey, Query{Limit: 10})
	if total != 2 || left[0].Data["status"] != FileKept || left[1].Data["status"] != FileKept {
		t.Fatalf("left = %+v", left)
	}

	q, _ := st.Repos().Create(ctx, storage.Repo{Name: "plan"}) // no folder: nothing kept, no error to the chat
	if _, err := s.SaveFile(ctx, q.ID, src, "a.pdf", "", ""); !errors.Is(err, ErrNoFolder) {
		t.Fatalf("no folder = %v", err)
	}
}
