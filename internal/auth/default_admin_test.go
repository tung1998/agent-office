package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
)

func TestDefaultAdmin(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newService(t)

	created, err := svc.EnsureDefaultAdmin(ctx)
	if err != nil || !created {
		t.Fatalf("first run: created=%v err=%v", created, err)
	}
	if created, _ := svc.EnsureDefaultAdmin(ctx); created {
		t.Fatal("made a second default admin")
	}
	def, pending := svc.PendingDefault(ctx)
	if !pending || def.Role != storage.RoleAdmin || !def.MustChange {
		t.Fatalf("pending default = %+v, %v", def, pending)
	}

	if _, err := svc.Login(ctx, "admin", "wrong", auth.ClientMeta{IP: "192.168.1.9"}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("bad password: %v", err)
	}
	res, err := svc.Login(ctx, "admin", "admin", auth.ClientMeta{IP: "192.168.1.9"})
	if err != nil || !res.User.MustChange {
		t.Fatalf("default login: %+v %v", res.User, err)
	}

	// setting it up: a real email and a strong password
	if _, err := svc.SetupAccount(ctx, def.ID, "not-an-email", "", "a-long-password-1", res.Session.ID); !errors.Is(err, auth.ErrInvalidEmail) {
		t.Fatalf("bad email: %v", err)
	}
	if _, err := svc.SetupAccount(ctx, def.ID, "boss@example.com", "", "short", res.Session.ID); !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}
	other, _ := svc.Login(ctx, "admin", "admin", auth.ClientMeta{IP: "127.0.0.1"})
	u, err := svc.SetupAccount(ctx, def.ID, " Boss@Example.com ", "Boss", "a-long-password-1", res.Session.ID)
	if err != nil || u.Email != "boss@example.com" || u.Name != "Boss" || u.MustChange {
		t.Fatalf("setup = %+v, %v", u, err)
	}
	if _, pending := svc.PendingDefault(ctx); pending {
		t.Fatal("still pending after setup")
	}
	if _, _, err := svc.Authenticate(ctx, other.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("another session of the default admin survived the setup")
	}
	if _, _, err := svc.Authenticate(ctx, res.Token); err != nil {
		t.Fatalf("the session that set it up was dropped: %v", err)
	}
	if _, err := svc.SetupAccount(ctx, def.ID, "x@example.com", "", "a-long-password-2", res.Session.ID); !errors.Is(err, auth.ErrSetUp) {
		t.Fatalf("setup twice: %v", err)
	}

	// the old login is gone, the new one works
	if _, err := svc.Login(ctx, "admin", "admin", auth.ClientMeta{IP: "127.0.0.1"}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("admin / admin after setup: %v", err)
	}
	if _, err := svc.Login(ctx, "boss@example.com", "a-long-password-1", auth.ClientMeta{IP: "10.0.0.5"}); err != nil {
		t.Fatalf("new login: %v", err)
	}
	if n, _ := st.Users().Count(ctx); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestDefaultAdminNotMadeWhenAccountsExist(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "a@example.com", Role: storage.RoleAdmin, Password: "a-long-password-1"}, "test"); err != nil {
		t.Fatal(err)
	}
	if created, err := svc.EnsureDefaultAdmin(ctx); err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if _, pending := svc.PendingDefault(ctx); pending {
		t.Fatal("pending without a default admin")
	}
}
