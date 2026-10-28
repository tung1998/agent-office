package packs_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/templates/packs"
)

// ADR-199: the library lists its bundles; a root pack writes its bundle (but
// the other packs), a unit pack its pack and the skills it lists; nothing is
// overwritten; a new repo is committed.
func TestLibrary(t *testing.T) {
	list, err := packs.Bundles()
	if err != nil || !slices.ContainsFunc(list, func(b packs.Bundle) bool { return b.Name == "@agentoffice/startup" }) ||
		!slices.ContainsFunc(list, func(b packs.Bundle) bool { return b.Name == "@agentoffice/ngoai-ngu" }) {
		t.Fatalf("bundles: %+v %v", list, err)
	}
	if _, e, err := packs.Find("@agentoffice/startup"); err != nil || !e.Root {
		t.Fatalf("root: %+v %v", e, err)
	}
	if _, _, err := packs.Find("@agentoffice/startup/nope"); err == nil {
		t.Fatal("found a pack that is not there")
	}
	if !packs.IsRef("@agentoffice/startup/marketing") || packs.IsRef("packs/x.json") {
		t.Fatal("IsRef")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	ctx := context.Background()
	repo := func() string {
		d := t.TempDir()
		exec.Command("git", "-C", d, "init", "-q", "-b", "main").Run()
		return d
	}
	hq := repo()
	if _, err := packs.Write(ctx, "@agentoffice/startup", hq, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"office.pack.json", ".claude/skills/van-hanh/SKILL.md", "docs/roadmap.md"} {
		if _, err := os.Stat(filepath.Join(hq, f)); err != nil {
			t.Fatalf("root: %s missing", f)
		}
	}
	for _, f := range []string{"bundle.json", "packs"} {
		if _, err := os.Stat(filepath.Join(hq, f)); err == nil {
			t.Fatalf("root: %s written (the unit packs stay in the library)", f)
		}
	}
	if out, _ := exec.Command("git", "-C", hq, "status", "--porcelain").Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("not committed: %s", out)
	}
	if _, err := packs.Write(ctx, "@agentoffice/startup", hq, false); !errors.Is(err, packs.ErrExists) {
		t.Fatalf("wrote over the repo: %v", err)
	}
	// a second root writes the same bundle with its own pack (ADR-204)
	sup := repo()
	if _, err := packs.Write(ctx, "@agentoffice/startup/giam-sat", sup, true); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(sup, "office.pack.json")); !strings.Contains(string(raw), "Văn phòng giám sát") {
		t.Fatal("giam-sat: not its own pack")
	}
	if _, err := os.Stat(filepath.Join(sup, ".claude/skills/to-chuc/vai.md")); err != nil {
		t.Fatal("giam-sat: the bundle's skills missing")
	}
	// the learner's bundle: its skills and notebook folders come with it
	learn := repo()
	if _, err := packs.Write(ctx, "@agentoffice/ngoai-ngu", learn, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"office.pack.json", ".claude/skills/ghi-bai/SKILL.md", ".claude/skills/on-tap/SKILL.md", ".claude/skills/ra-de/SKILL.md", "docs/ho-so.md", "bai-hoc/README.md"} {
		if _, err := os.Stat(filepath.Join(learn, f)); err != nil {
			t.Fatalf("ngoai-ngu: %s missing", f)
		}
	}
	if _, e, err := packs.Find("@agentoffice/company/marketing"); err != nil || e.Ref != "@agentoffice/startup/marketing" {
		t.Fatalf("old name: %+v %v", e, err)
	}
	unit := repo()
	paths, err := packs.Write(ctx, "@agentoffice/startup/marketing", unit, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(unit, "office.pack.json"))
	if !strings.Contains(string(raw), "Trưởng phòng Marketing") || !strings.Contains(strings.Join(paths, " "), ".claude/skills/phat-trien/SKILL.md") {
		t.Fatalf("unit: %v", paths)
	}
	// a skill's other files (what it reads or runs) come with it
	for _, f := range []string{".claude/skills/to-chuc/vai.md", ".claude/skills/tri-thuc/lint.mjs"} {
		if _, err := os.Stat(filepath.Join(unit, f)); err != nil {
			t.Fatalf("unit: %s missing", f)
		}
	}
}

// ADR-206: a project's copy of its template's files comes back to the
// library's (pack and skills, not its documents), committed on their own.
func TestSyncFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	ctx := context.Background()
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init", "-q", "-b", "main").Run()
	if _, err := packs.Write(ctx, "@agentoffice/startup", dir, true); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(dir, ".claude/skills/to-chuc/SKILL.md")
	os.WriteFile(skill, []byte("cũ"), 0o644) // the template as it was when the project was made
	exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@x", "commit", "-qam", "old").Run()
	os.WriteFile(filepath.Join(dir, "docs/roadmap.md"), []byte("kế hoạch của project"), 0o644)
	exec.Command("git", "-C", dir, "add", "docs/roadmap.md").Run()      // the project's own work, staged
	got, err := packs.SyncFiles(ctx, "@agentoffice/company", dir, true) // the old name finds it too
	if err != nil || strings.Join(got, ",") != ".claude/skills/to-chuc/SKILL.md" {
		t.Fatalf("preview: %v %v", got, err)
	}
	if b, _ := os.ReadFile(skill); string(b) != "cũ" {
		t.Fatal("a preview wrote")
	}
	if _, err := packs.SyncFiles(ctx, "@agentoffice/startup", dir, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(skill); string(b) == "cũ" {
		t.Fatal("the skill is still the project's old copy")
	}
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if strings.TrimSpace(string(out)) != "M  docs/roadmap.md" {
		t.Fatalf("status after sync: %q", out)
	}
	if log, _ := exec.Command("git", "-C", dir, "log", "-1", "--name-only", "--format=%s").Output(); !strings.Contains(string(log), "Đồng bộ mẫu") || strings.Contains(string(log), "roadmap") {
		t.Fatalf("commit: %s", log)
	}
}
