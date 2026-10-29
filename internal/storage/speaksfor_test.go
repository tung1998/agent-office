package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-194: a scheduled automation that is not administrator speaks for its
// owner while that account is active; one with no owner (an import), a
// disabled owner, or a webhook speaks for nobody.
func TestSpeaksForOwner(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	u, _ := st.Users().Create(ctx, storage.User{Email: "lead@x.io", Name: "Lead", Role: storage.RoleMember})
	mine, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "a", Source: "schedule", Action: "chat", CreatedBy: "lead@x.io"})
	imported, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "b", Source: "schedule", Action: "chat", CreatedBy: "import"})
	job := func(a storage.Automation, trig string) storage.Job {
		return storage.Job{Origin: "automation", OriginID: a.ID, Trigger: trig}
	}
	if as, ok := storage.SpeaksFor(ctx, st, job(mine, "schedule")); !ok || as != "human:lead@x.io" {
		t.Fatalf("owner: %q %v", as, ok)
	}
	if _, ok := storage.SpeaksFor(ctx, st, job(imported, "schedule")); ok {
		t.Fatal("an import speaks for someone")
	}
	if _, ok := storage.SpeaksFor(ctx, st, job(mine, "webhook")); ok {
		t.Fatal("a webhook speaks for its owner")
	}
	st.Users().SetDisabled(ctx, u.ID, true)
	if _, ok := storage.SpeaksFor(ctx, st, job(mine, "schedule")); ok {
		t.Fatal("a disabled owner still speaks")
	}
}

// ADR-232: a bot message speaks for the account its writer's platform id is
// linked to, while that account is active; an unlinked id, a mere name, or a
// mismatched platform speaks for nobody.
func TestSpeaksForLinkedSender(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	u, _ := st.Users().Create(ctx, storage.User{Email: "lead@x.io", Name: "Lead", Role: storage.RoleAdmin})
	st.Users().SetChatIDs(ctx, u.ID, "123", "")
	job := func(trig, payload string) storage.Job {
		return storage.Job{Origin: "automation", Trigger: trig, Payload: payload}
	}
	linked := `{"sender":{"kind":"discord","id":"123","username":"lead"}}`
	if as, ok := storage.SpeaksFor(ctx, st, job("discord", linked)); !ok || as != "human:lead@x.io" {
		t.Fatalf("linked: %q %v", as, ok)
	}
	for name, j := range map[string]storage.Job{
		"unlinked":  job("discord", `{"sender":{"kind":"discord","id":"999"}}`),
		"name only": job("discord", `{"sender":{"kind":"discord","username":"lead"}}`),
		"platform":  job("telegram", linked),
		"webhook":   job("webhook", linked),
	} {
		if _, ok := storage.SpeaksFor(ctx, st, j); ok {
			t.Fatalf("%s speaks for someone", name)
		}
	}
	st.Users().SetDisabled(ctx, u.ID, true)
	if _, ok := storage.SpeaksFor(ctx, st, job("discord", linked)); ok {
		t.Fatal("a disabled account still speaks")
	}
}
