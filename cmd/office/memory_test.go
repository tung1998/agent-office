package main

import (
	"testing"

	"github.com/tung1998/agent-office/internal/memory"
)

// ADR-215: what a review model answers becomes changes to the notes; other
// lines are ignored.
func TestParseReview(t *testing.T) {
	got := parseReview("Here you go:\n- ADD deploy | build, release | build bằng `make dist`, không dùng go build\nADD - | - | repo là bản private\nEDIT mem_1 | chạy pnpm test | trước khi báo xong\nDROP mem_2 (cũ)\nNONE\nADD thiếu")
	want := []memory.Op{
		{Op: "add", Topic: "deploy", Summary: "build, release", Text: "build bằng `make dist`, không dùng go build"},
		{Op: "add", Text: "repo là bản private"},
		{Op: "edit", ID: "mem_1", Text: "chạy pnpm test | trước khi báo xong"},
		{Op: "drop", ID: "mem_2"},
	}
	if len(got) != len(want) {
		t.Fatalf("ops = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("op %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// ADR-218: a LINK line is a link to keep: kind, title, url, note.
func TestParseReviewLink(t *testing.T) {
	got := parseReview("LINK tai-ve | Bản build 0.2 | https://x.io/b.zip | bản cho Chrome\nLINK web | - | https://x.io\nLINK web | thiếu url")
	want := []memory.Op{
		{Op: "link", Topic: "tai-ve", Summary: "Bản build 0.2", Text: "https://x.io/b.zip", ID: "bản cho Chrome"},
		{Op: "link", Topic: "web", Text: "https://x.io"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ops = %+v", got)
	}
}
