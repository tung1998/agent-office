package channels

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// A bot's /goal (ADR-271): a goal for the message, its check an admin's
// only; status and clear say so when nothing runs.
func TestGoalCommand(t *testing.T) {
	ctx := context.Background()
	ch := storage.Channel{Kind: "discord", Approvers: []string{"u-admin"}}
	m := &Manager{}
	if cmd, arg, ok := command("/goal sửa test"); !ok || cmd != "goal" || arg != "sửa test" {
		t.Fatalf("parse goal = %q %q %v", cmd, arg, ok)
	}
	if cmd, arg, ok := command("/btw đang làm gì?"); !ok || cmd != "btw" || arg != "đang làm gì?" {
		t.Fatalf("parse btw = %q %q %v", cmd, arg, ok)
	}
	g, text, reply := m.goalCommand(ctx, ch, Incoming{UserID: "u-other"}, "k", "sửa test --rounds 3")
	if reply != "" || text != "sửa test" || g == nil || !g.Judge || g.MaxRounds != 3 || g.Check != "" {
		t.Fatalf("goal = %+v %q %q", g, text, reply)
	}
	if _, _, reply := m.goalCommand(ctx, ch, Incoming{UserID: "u-other"}, "k", `sửa test --check "go test ./..."`); !strings.Contains(reply, "Chỉ Admin") {
		t.Fatalf("a non-admin's check = %q", reply)
	}
	if g, _, reply := m.goalCommand(ctx, ch, Incoming{UserID: "u-admin"}, "k", `sửa test --check "go test ./..."`); reply != "" || g.Check != "go test ./..." {
		t.Fatalf("an admin's check = %+v %q", g, reply)
	}
	if _, _, reply := m.goalCommand(ctx, ch, Incoming{UserID: "u-admin"}, "k", "sửa --rounds 99"); !strings.Contains(reply, "số vòng") {
		t.Fatalf("too many rounds = %q", reply)
	}
	if _, _, reply := m.goalCommand(ctx, ch, Incoming{}, "k", "status"); !strings.Contains(reply, "Chưa có mục tiêu") {
		t.Fatalf("status = %q", reply)
	}
	if _, _, reply := m.goalCommand(ctx, ch, Incoming{}, "k", "clear"); !strings.Contains(reply, "Không có mục tiêu") {
		t.Fatalf("clear = %q", reply)
	}
}
