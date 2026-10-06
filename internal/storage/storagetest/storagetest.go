// Package storagetest is the contract every storage driver must pass.
package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Run executes the contract suite. newStore must return a fresh, migrated store.
func Run(t *testing.T, newStore func(t *testing.T) storage.Store) {
	t.Run("users", func(t *testing.T) { testUsers(t, newStore(t)) })
	t.Run("sessions", func(t *testing.T) { testSessions(t, newStore(t)) })
	t.Run("audit", func(t *testing.T) { testAudit(t, newStore(t)) })
	t.Run("providers", func(t *testing.T) { testProviders(t, newStore(t)) })
	t.Run("org", func(t *testing.T) { testOrg(t, newStore(t)) })
	t.Run("tx", func(t *testing.T) { testTx(t, newStore(t)) })
	t.Run("helper projects", func(t *testing.T) { testHelperProjects(t, newStore(t)) })
	t.Run("mcp servers", func(t *testing.T) { testMCPServers(t, newStore(t)) })
	t.Run("mcp calls", func(t *testing.T) { testMCPCalls(t, newStore(t)) })
}

func testUsers(t *testing.T, s storage.Store) {
	ctx := context.Background()
	users := s.Users()

	n, err := users.Count(ctx)
	if err != nil || n != 0 {
		t.Fatalf("Count on empty = %d, %v", n, err)
	}
	u, err := users.Create(ctx, storage.User{Email: "Admin@Example.com", Name: "Admin", Role: storage.RoleAdmin, PasswordHash: "h1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == "" || u.CreatedAt.IsZero() {
		t.Fatalf("Create did not fill ID/CreatedAt: %+v", u)
	}
	if _, err := users.Create(ctx, storage.User{Email: "admin@example.com", Role: storage.RoleMember, PasswordHash: "x"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate email (case-insensitive) err = %v, want ErrConflict", err)
	}
	got, err := users.GetByEmail(ctx, "ADMIN@example.com")
	if err != nil || got.ID != u.ID || got.Role != storage.RoleAdmin {
		t.Fatalf("GetByEmail = %+v, %v", got, err)
	}
	if _, err := users.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetByEmail missing err = %v", err)
	}
	if err := users.UpdatePassword(ctx, u.ID, "h2"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	if err := users.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := users.TouchLogin(ctx, u.ID, now); err != nil {
		t.Fatalf("TouchLogin: %v", err)
	}
	got, err = users.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PasswordHash != "h2" || !got.Disabled || got.LastLoginAt == nil || !got.LastLoginAt.Equal(now) {
		t.Fatalf("after updates = %+v", got)
	}
	if err := users.UpdatePassword(ctx, "usr_missing", "x"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("UpdatePassword missing err = %v", err)
	}
	list, err := users.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %d, %v", len(list), err)
	}
}

func testSessions(t *testing.T, s storage.Store) {
	ctx := context.Background()
	u, err := s.Users().Create(ctx, storage.User{Email: "a@b.c", Role: storage.RoleMember, PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	sess, err := s.Sessions().Create(ctx, storage.Session{
		UserID: u.ID, TokenHash: "th1", CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now, UserAgent: "ua", IP: "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Sessions().GetByTokenHash(ctx, "th1")
	if err != nil || got.ID != sess.ID || got.UserID != u.ID || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("GetByTokenHash = %+v, %v", got, err)
	}
	if _, err := s.Sessions().GetByTokenHash(ctx, "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	if err := s.Sessions().Touch(ctx, sess.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	// expired session
	if _, err := s.Sessions().Create(ctx, storage.Session{UserID: u.ID, TokenHash: "th2", CreatedAt: now, ExpiresAt: now.Add(-time.Second), LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	n, err := s.Sessions().DeleteExpired(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpired = %d, %v", n, err)
	}
	keep, _ := s.Sessions().Create(ctx, storage.Session{UserID: u.ID, TokenHash: "th3", CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now})
	if err := s.Sessions().DeleteForUserExcept(ctx, u.ID, keep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sessions().GetByTokenHash(ctx, "th1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("DeleteForUserExcept kept th1: %v", err)
	}
	if _, err := s.Sessions().GetByTokenHash(ctx, "th3"); err != nil {
		t.Fatalf("DeleteForUserExcept removed kept session: %v", err)
	}
	if err := s.Sessions().DeleteForUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sessions().GetByTokenHash(ctx, "th3"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after DeleteForUser err = %v", err)
	}
}

func testAudit(t *testing.T, s storage.Store) {
	ctx := context.Background()
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	add := func(e storage.AuditEntry) {
		t.Helper()
		if e.At.IsZero() {
			e.At = at
		}
		if err := s.Audit().Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	add(storage.AuditEntry{Actor: "system", Action: "user.create", Target: "usr_1", Detail: map[string]any{"role": "admin"}, ActorKind: "system", OK: true})
	add(storage.AuditEntry{Action: "automation.update", ActorKind: "agent", ActorName: "Lead", ApprovedBy: "a@x.io", Via: "chat",
		ProjectID: "prj_1", ConversationID: "cnv_1", JobID: "job_1", ActionID: "act_1", Resource: "automation", ResourceID: "aut_1",
		Before: map[string]any{"name": "a"}, After: map[string]any{"name": "b"}, OK: true})
	add(storage.AuditEntry{Action: "automation.update", ActorKind: "human", ActorName: "a@x.io", Via: "ui", ProjectID: "prj_1",
		Resource: "automation", ResourceID: "aut_1", OK: false})

	got, err := s.Audit().List(ctx, storage.AuditFilter{ProjectID: "prj_1"})
	if err != nil || len(got) != 2 {
		t.Fatalf("project filter = %d %v", len(got), err)
	}
	agent, _ := s.Audit().List(ctx, storage.AuditFilter{ActorKind: "agent"})
	if len(agent) != 1 || agent[0].ApprovedBy != "a@x.io" || agent[0].JobID != "job_1" || agent[0].After["name"] != "b" || agent[0].Before["name"] != "a" || !agent[0].OK {
		t.Fatalf("agent row = %+v", agent)
	}
	if got, _ := s.Audit().List(ctx, storage.AuditFilter{ConversationID: "cnv_1"}); len(got) != 1 {
		t.Fatalf("conversation filter = %d", len(got))
	}

	// same timestamp: paging by id neither repeats nor skips
	seen := map[string]bool{}
	before := ""
	for range 5 {
		page, err := s.Audit().List(ctx, storage.AuditFilter{Limit: 1, BeforeID: before})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if seen[page[0].ID] {
			t.Fatalf("page repeated %s", page[0].ID)
		}
		seen[page[0].ID] = true
		before = page[0].ID
	}
	if len(seen) != 3 {
		t.Fatalf("paged %d rows, want 3", len(seen))
	}

	byKind, err := s.Audit().Count(ctx, storage.AuditFilter{ProjectID: "prj_1"}, "kind")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]storage.AuditCount{}
	for _, c := range byKind {
		counts[c.Key] = c
	}
	if counts["agent"].Count != 1 || counts["human"].Count != 1 || counts["human"].Failed != 1 {
		t.Fatalf("count by kind = %+v", byKind)
	}
	if _, err := s.Audit().Count(ctx, storage.AuditFilter{}, "nope"); err == nil {
		t.Fatal("unknown group accepted")
	}
}
