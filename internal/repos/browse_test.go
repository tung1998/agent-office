package repos_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/repos"
)

func TestListDirs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "shop", ".git"), 0o755)
	os.WriteFile(filepath.Join(root, "shop", "package.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(root, "Blog", "src"), 0o755)
	os.MkdirAll(filepath.Join(root, ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644)

	l, err := repos.ListDirs(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 || l.Entries[0].Name != "Blog" || l.Entries[1].Name != "shop" {
		t.Fatalf("entries = %+v", l.Entries)
	}
	shop := l.Entries[1]
	if !shop.IsProject || len(shop.Markers) != 2 || shop.HasChild {
		t.Fatalf("shop = %+v", shop)
	}
	if blog := l.Entries[0]; blog.IsProject || !blog.HasChild {
		t.Fatalf("blog = %+v", blog)
	}
	if l.Parent == "" {
		t.Fatal("parent missing")
	}
	if l, _ := repos.ListDirs(root, true); len(l.Entries) != 3 {
		t.Fatalf("with hidden = %d", len(l.Entries))
	}
	if _, err := repos.ListDirs(filepath.Join(root, "file.txt"), false); err == nil {
		t.Fatal("file must fail")
	}
	if len(repos.Shortcuts()) == 0 {
		t.Fatal("no shortcuts")
	}
}
