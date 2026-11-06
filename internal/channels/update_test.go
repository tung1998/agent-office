package channels

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

type fakeUpdater struct {
	started   []string
	cancelled int
}

func (f *fakeUpdater) Status(context.Context) string { return "Đang chờ hết việc: 1 quy trình" }
func (f *fakeUpdater) WhenIdle(_ context.Context, by string) (string, error) {
	f.started = append(f.started, by)
	return "Office đang build bản mới, sẽ khởi động lại khi rảnh.", nil
}
func (f *fakeUpdater) Cancel() error {
	f.cancelled++
	if f.cancelled > 1 {
		return errors.New("không có bản cập nhật nào đang chờ")
	}
	return nil
}

// /update: the bot's admins start (when idle) or cancel office's update,
// anyone sees where it stands; the command is in the menu only when office
// can update itself (ADR-255).
func TestUpdateCommand(t *testing.T) {
	ctx := context.Background()
	ch := storage.Channel{Kind: "discord", Approvers: []string{"u-admin"}}
	m := &Manager{}
	if cmd, arg, ok := command("/update cancel"); !ok || cmd != "update" || arg != "cancel" {
		t.Fatalf("parse = %q %q %v", cmd, arg, ok)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "", "an"); !strings.Contains(out, "không tự cập nhật") {
		t.Fatalf("no updater = %q", out)
	}
	up := &fakeUpdater{}
	m.SetUpdater(up)
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-other"}, "", "bob"); !strings.Contains(out, "Chỉ người") || len(up.started) != 0 {
		t.Fatalf("a non-admin started an update: %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-other"}, "status", "bob"); !strings.Contains(out, "chờ hết việc") {
		t.Fatalf("status = %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "", "an"); !strings.Contains(out, "khi rảnh") || !slices.Equal(up.started, []string{"discord:an"}) {
		t.Fatalf("admin start = %q %v", out, up.started)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-other"}, "cancel", "bob"); !strings.Contains(out, "Chỉ người") || up.cancelled != 0 {
		t.Fatalf("a non-admin cancelled: %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "cancel", "an"); !strings.Contains(out, "Đã hủy") {
		t.Fatalf("cancel = %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "cancel", "an"); !strings.Contains(out, "không có") {
		t.Fatalf("cancel twice = %q", out)
	}
	if !slices.Contains(Reserved, "update") {
		t.Fatal("a custom command could take /update")
	}
}

type fakeCLI struct{ updated []string }

func (f *fakeCLI) CLIStatus(context.Context) string { return "Codex 0.166.0 (có bản 0.167.0)" }
func (f *fakeCLI) UpdateCLI(_ context.Context, tool, by string) (string, error) {
	if tool != "codex" {
		return "", errors.New("chưa cài " + tool)
	}
	f.updated = append(f.updated, tool+"|"+by)
	return "Codex: chờ 1 lượt đang dùng chạy xong rồi cập nhật", nil
}

// /update cli [tool] (ADR-259): anyone sees the CLIs, the bot's admins update
// one; the menu has /update even where office cannot update itself.
func TestUpdateCLICommand(t *testing.T) {
	ctx := context.Background()
	ch := storage.Channel{Kind: "telegram", Approvers: []string{"u-admin"}}
	m := &Manager{}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "cli codex", "an"); !strings.Contains(out, "không cập nhật CLI") {
		t.Fatalf("no updater = %q", out)
	}
	cli := &fakeCLI{}
	m.SetCLIUpdater(cli)
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-other"}, "cli", "bob"); !strings.Contains(out, "0.167.0") {
		t.Fatalf("status = %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-other"}, "cli codex", "bob"); !strings.Contains(out, "Chỉ người") || len(cli.updated) != 0 {
		t.Fatalf("a non-admin updated: %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "CLI Codex", "an"); !strings.Contains(out, "chờ 1 lượt") || !slices.Equal(cli.updated, []string{"codex|telegram:an"}) {
		t.Fatalf("admin = %q %v", out, cli.updated)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "cli gemini", "an"); !strings.Contains(out, "chưa cài gemini") {
		t.Fatalf("unknown = %q", out)
	}
	if out := m.updateCommand(ctx, ch, Incoming{UserID: "u-admin"}, "", "an"); !strings.Contains(out, "không tự cập nhật") {
		t.Fatalf("office update without an updater = %q", out)
	}
}
