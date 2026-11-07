package burn_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/storage"
)

// Findings of one kind on the same files, or named into a waiting piece, go
// into that one piece: a worker fixes them together, not a piece each.
func TestBurnAddFoldsSameKindIntoOnePiece(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ends := time.Now().Add(time.Hour)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxParallel: 1, ResultMode: "branch", EndsAt: &ends, State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	add := func(in burn.ToolInput) string {
		t.Helper()
		out, err := f.svc.Tool(ctx, sc, "burn_add", in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := add(burn.ToolInput{Title: "docs/PLAN.md dòng 122 vẫn ghi Chưa làm", Kind: "unfinished", Detail: "Where: docs/PLAN.md:122"})
	// the same finding, worded otherwise: folded, not a second piece
	if out := add(burn.ToolInput{Title: "PLAN.md vẫn ghi Chưa làm cho thông báo sự cố", Kind: "unfinished", Detail: "Where: docs/PLAN.md:122", Priority: "high"}); !strings.Contains(out, "Đã gộp") {
		t.Fatalf("same files, same kind: %s", out)
	}
	// another kind on the same file stays its own piece
	if out := add(burn.ToolInput{Title: "PLAN.md link hỏng", Kind: "bug", Detail: "Where: docs/PLAN.md:5"}); strings.Contains(out, "Đã gộp") {
		t.Fatalf("another kind folded: %s", out)
	}
	// one kind of flaw in another file, named into the waiting piece
	stale := add(burn.ToolInput{Title: "EnvPanel save() stale response overwrites file", Kind: "bug", Detail: "Where: dashboard/components/EnvPanel.vue:40"})
	id := stale[strings.Index(stale, "bit_"):strings.LastIndex(stale, ".")]
	if out := add(burn.ToolInput{Title: "AutomationForm preview stale fetch", Kind: "bug", Detail: "Where: dashboard/components/AutomationForm.vue:12", Item: id}); !strings.Contains(out, id) {
		t.Fatalf("into %s: %s", id, out)
	}
	if out := add(burn.ToolInput{Title: "AutomationForm  preview stale fetch", Kind: "bug"}); !strings.Contains(out, "Đã có") {
		t.Fatalf("a finding folded in, recorded again: %s", out)
	}
	// a different finding on the same file and kind stays its own piece
	if out := add(burn.ToolInput{Title: "EnvPanel thiếu nút huỷ", Kind: "bug", Detail: "Where: dashboard/components/EnvPanel.vue:80"}); strings.Contains(out, "Đã gộp") {
		t.Fatalf("an unrelated finding folded: %s", out)
	}
	if _, err := f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "x", Kind: "bug", Detail: "y", Item: "bit_nope"}); err == nil {
		t.Fatal("folded into a piece that does not exist")
	}
	items, _ := f.st.Burn().Items(ctx, b.ID)
	var pieces []storage.BurnItem
	for _, it := range items {
		if it.Kind != "" {
			pieces = append(pieces, it)
		}
	}
	if len(pieces) != 4 {
		t.Fatalf("pieces = %d, want 4: %+v", len(pieces), pieces)
	}
	for _, it := range pieces {
		switch {
		case strings.Contains(first, it.ID):
			if !strings.Contains(it.Detail, "Gộp thêm: PLAN.md vẫn ghi") || it.Priority != 2 {
				t.Fatalf("plan piece: %+v", it)
			}
		case it.ID == id:
			if !strings.Contains(it.Detail, "AutomationForm.vue") {
				t.Fatalf("stale piece: %+v", it)
			}
		}
	}
}
