package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, Dir, "hieu-biet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, Dir, "quyet-dinh.md"), []byte("# Quyết định\n\n## Thanh toán\nDùng Stripe cho thẻ, PayPal cho ví.\n\n## Triển khai\nDeploy bằng Fly.io mỗi tối.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "khach.md"), []byte("# Khách hàng\nKhách sỉ thanh toán chuyển khoản.\n"), 0o644)
	os.WriteFile(filepath.Join(root, Dir, "note.txt"), []byte("stripe"), 0o644)

	hits, err := Search(root, "thanh toan stripe", 5)
	if err != nil || len(hits) != 1 || hits[0].Path != "docs/wiki/quyet-dinh.md" || hits[0].Heading != "Thanh toán" || !strings.Contains(hits[0].Text, "Stripe") {
		t.Fatalf("hits = %+v %v", hits, err)
	}
	// a word no page has: the sections with most words
	if hits, _ = Search(root, "thanh toan loi", 5); len(hits) != 2 {
		t.Fatalf("partial = %+v", hits)
	}
	if hits, _ = Search(t.TempDir(), "stripe", 5); hits != nil {
		t.Fatalf("no wiki = %+v", hits)
	}
}

func TestSplitLongSection(t *testing.T) {
	para := strings.Repeat("chữ ", 300)
	secs := split("a.md", "# A\n"+para+"\n\n"+para+"\n\n"+para)
	if len(secs) < 2 {
		t.Fatalf("sections = %d", len(secs))
	}
	for _, s := range secs {
		if len(s.text) > maxSection || s.heading != "A" {
			t.Fatalf("section = %d %q", len(s.text), s.heading)
		}
	}
}
