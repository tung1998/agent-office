package burn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestVerdict(t *testing.T) {
	for text, want := range map[string]string{
		"KẾT LUẬN: ĐỒNG Ý\nổn":            "yes",
		"**KẾT LUẬN: KHÔNG ĐỒNG Ý**\nsai": "no",
		"Ket luan: dong y":                "yes",
		"VERDICT: AGREE\nfine":            "yes",
		"**VERDICT: DISAGREE**\nwrong":    "no",
		"Tôi nghĩ là được":                "unclear",
		// analysis first, conclusion last — the reviewer agent's usual style
		"Phân tích:\ndòng 1\ndòng 2\ndòng 3\ndòng 4\ndòng 5\nKẾT LUẬN: ĐỒNG Ý": "yes",
		// first line wins even if a later line quotes the opposite verdict
		"KẾT LUẬN: KHÔNG ĐỒNG Ý\nbài kia dùng `KẾT LUẬN: ĐỒNG Ý` ở dòng cuối": "no",
		// a mid-paragraph mention of agreement/disagreement is not a verdict line
		"KẾT LUẬN: ĐỒNG Ý\nmình không đồng ý với X nhưng tổng thể ổn": "yes",
		// no line starts with the KẾT LUẬN prefix at all
		"Đây là phân tích tự do, không có kết luận rõ ràng nào cả.":    "unclear",
		"Analysis:\nline 1\nline 2\nline 3\nline 4\nVERDICT: DISAGREE": "no",
		"I agree with the approach overall":                            "unclear",
	} {
		if got := verdictOf(text); got != want {
			t.Errorf("verdict(%q) = %s, want %s", text, got, want)
		}
	}
}

// A run's summary: what this run did, what is left (ADR-120).
func TestSummary(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute)
	b := storage.BurnSession{StartedAt: &start, ResultMode: "branch", RunBranch: "burn/a"}
	items := []storage.BurnItem{
		{Title: "Sửa lỗi A", Status: "done", Branch: "burn/a", Summary: "đã sửa", CostUSD: 1.5, UpdatedAt: time.Now()},
		{Title: "Cũ", Status: "done", UpdatedAt: start.Add(-time.Hour)},
		{Title: "Làm B", Status: "paused", UpdatedAt: time.Now()},
		{Title: "C", Status: "failed", Summary: "không build được", UpdatedAt: time.Now()},
		{Title: "D", Status: "found"},
	}
	s := summary(b, items, "demo", "dừng hẳn", time.Now())
	for _, want := range []string{"Burn demo đã dừng", "1 giờ 30 phút", "Sửa lỗi A", "Kết quả: nhánh `burn/a`", "Làm B", "không build được", "$1.50", "1 việc tìm thấy"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Cũ") {
		t.Errorf("an earlier run's piece is in it:\n%s", s)
	}
}

// A result agreed with an acceptance line still open is not agreed: the
// evidence wins over the verdict (ADR-223). Other stages read the verdict.
func TestAcceptedNeedsEveryCriterion(t *testing.T) {
	done := "VERDICT: AGREE\n- [x] nút lưu — app/save.vue:12\nFollow-up: đổi tên biến"
	open := "VERDICT: AGREE\n- [x] nút lưu — app/save.vue:12\n- [ ] test — chưa có"
	if !accepted("result", done) || accepted("result", open) || !accepted("plan", open) || accepted("result", "VERDICT: DISAGREE\n- [x] a") {
		t.Fatal("accepted: agreed and every criterion met")
	}
}

// The result review is told which checks already passed, and asks for the
// acceptance with evidence.
func TestResultReviewPromptChecks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	p := reviewPrompt(storage.BurnSession{}, storage.BurnItem{Worktree: dir, Title: "x"}, "result", "")
	if !strings.Contains(p, "they passed: go build ./..., go vet ./...") || !strings.Contains(p, "- [ ] <what it asks>") {
		t.Fatalf("prompt:\n%s", p)
	}
	if p := reviewPrompt(storage.BurnSession{}, storage.BurnItem{Title: "x"}, "result", ""); !strings.Contains(p, "No automatic check ran") {
		t.Fatalf("no checks: the reviewer verifies itself:\n%s", p)
	}
}
