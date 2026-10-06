package attach

import (
	"errors"
	"strings"
	"testing"
)

var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00")

func TestSaveResolve(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	img, err := s.Save("p1", "a@x", "../../shot.png", png)
	if err != nil || img.Kind != "image" || img.Name != "shot.png" {
		t.Fatal(img, err)
	}
	doc, err := s.Save("p1", "a@x", "notes.md", []byte("# hi"))
	if err != nil || doc.Kind != "text" {
		t.Fatal(doc, err)
	}
	pdf, _ := s.Save("p1", "a@x", "a.pdf", []byte("%PDF-1.4\n..."))
	if pdf.Kind != "pdf" {
		t.Fatal(pdf)
	}
	if _, err := s.Save("p1", "a@x", "a.bin", []byte{0, 1, 2, 3}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	files, err := s.Resolve("p1", []string{img.ID, doc.ID})
	if err != nil || len(files) != 2 || files[1].Text != "# hi" {
		t.Fatal(files, err)
	}
	if _, err := s.Resolve("p2", []string{img.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("other project must not use it")
	}
	if _, _, err := s.Get("../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	p := InlineText("Q", files, func(f File) bool { return f.Kind == "image" })
	if !strings.Contains(p, `<attachment name="notes.md">`) || !strings.Contains(p, "shot.png") {
		t.Fatal(p)
	}
}
