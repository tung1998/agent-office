package burn

import (
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestIdleBacksOff(t *testing.T) {
	s := &Service{idle: 5 * time.Minute}
	for n, want := range map[int]time.Duration{1: 5 * time.Minute, 2: 10 * time.Minute, 3: 20 * time.Minute, 4: 40 * time.Minute, 5: time.Hour, 9: time.Hour} {
		if got := s.idleAfter(n); got != want {
			t.Errorf("idleAfter(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestPlanPromptDigsDeeper(t *testing.T) {
	b := storage.BurnSession{MaxSubagents: 2}
	p := planPrompt(b, nil, 0)
	for _, want := range []string{"subagent", "vùng đã xem"} {
		if !strings.Contains(p, want) {
			t.Errorf("first scan prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "liên tiếp") {
		t.Error("first scan should not mention empty scans")
	}
	if p2 := planPrompt(b, nil, 2); !strings.Contains(p2, "2 lần quét liên tiếp") {
		t.Errorf("repeat scan should say how many came back empty:\n%s", p2)
	}
}

func TestPlanPromptOrder(t *testing.T) {
	road := planPrompt(storage.BurnSession{Order: "roadmap"}, nil, 0)
	for _, want := range []string{"LỘ TRÌNH TRƯỚC", "chưa được duyệt", "thiết kế ngắn", "phần 1"} {
		if !strings.Contains(road, want) {
			t.Errorf("roadmap prompt lacks %q", want)
		}
	}
	if i, j := strings.Index(road, "Lộ trình"), strings.Index(road, "Lỗi chi tiết"); i < 0 || j < 0 || i > j {
		t.Errorf("roadmap should come before bugs")
	}
	bugs := planPrompt(storage.BurnSession{Order: "bugs"}, nil, 0)
	if i, j := strings.Index(bugs, "Lộ trình"), strings.Index(bugs, "Lỗi chi tiết"); i < 0 || j < 0 || j > i {
		t.Errorf("bugs order should list bugs first")
	}
	if strings.Contains(bugs, "LỘ TRÌNH TRƯỚC") {
		t.Error("bugs order should not push the roadmap first")
	}
	// an old row with no order behaves as roadmap
	if !strings.Contains(planPrompt(storage.BurnSession{}, nil, 0), "LỘ TRÌNH TRƯỚC") {
		t.Error("empty order should default to roadmap")
	}
}

func TestWorkPromptFeature(t *testing.T) {
	p := workPrompt(storage.BurnSession{}, storage.BurnItem{Kind: "unfinished", Title: "Thông báo sự cố: phần 1"}, false)
	if !strings.Contains(p, "đánh dấu tiến độ") {
		t.Errorf("a roadmap piece should update the roadmap docs:\n%s", p)
	}
}
