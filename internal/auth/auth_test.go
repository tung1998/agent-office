package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time          { return c.t }
func (c *clock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T) (*auth.Service, storage.Store, *clock) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
	svc := auth.NewService(st, auth.Options{
		SessionTTL:  7 * 24 * time.Hour,
		IdleTimeout: 24 * time.Hour,
		Now:         c.Now,
		Hasher:      auth.FastHasherForTests(),
	})
	return svc, st, c
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h := auth.DefaultHasher()
	enc, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$") {
		t.Fatalf("unexpected encoding %q", enc)
	}
	ok, err := h.Verify(enc, "correct horse battery")
	if err != nil || !ok {
		t.Fatalf("Verify good = %v, %v", ok, err)
	}
	ok, err = h.Verify(enc, "wrong password!")
	if err != nil || ok {
		t.Fatalf("Verify bad = %v, %v", ok, err)
	}
	enc2, _ := h.Hash("correct horse battery")
	if enc == enc2 {
		t.Fatal("hash must be salted")
	}
}

func TestCreateUserValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "bad", Role: storage.RoleAdmin, Password: "longenoughpw"}, "system"); !errors.Is(err, auth.ErrInvalidEmail) {
		t.Fatalf("bad email err = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "a@b.co", Role: storage.RoleAdmin, Password: "short"}, "system"); !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("weak password err = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "a@b.co", Role: "root", Password: "longenoughpw"}, "system"); !errors.Is(err, auth.ErrInvalidRole) {
		t.Fatalf("bad role err = %v", err)
	}
	u, err := svc.CreateUser(ctx, auth.NewUser{Email: " A@B.co ", Name: "A", Role: storage.RoleAdmin, Password: "longenoughpw"}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "a@b.co" {
		t.Fatalf("email not normalized: %q", u.Email)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "a@b.co", Role: storage.RoleMember, Password: "longenoughpw"}, "system"); !errors.Is(err, auth.ErrEmailTaken) {
		t.Fatalf("duplicate err = %v", err)
	}
}

func TestLoginAuthenticateLogout(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, auth.NewUser{Email: "admin@x.io", Role: storage.RoleAdmin, Password: "s3cret-password"}, "system")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Login(ctx, "Admin@X.io", "s3cret-password", auth.ClientMeta{IP: "1.1.1.1", UserAgent: "test"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(res.Token) < 40 || res.User.ID != u.ID {
		t.Fatalf("Login result = %+v", res)
	}
	got, _, err := svc.Authenticate(ctx, res.Token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	// token is not stored in clear
	if _, err := st.Sessions().GetByTokenHash(ctx, res.Token); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("raw token must not be stored")
	}
	if err := svc.Logout(ctx, res.Token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("after logout err = %v", err)
	}
	entries, _ := st.Audit().List(ctx, storage.AuditFilter{Limit: 10})
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.Action] = true
		if e.Action == "auth.login" && (e.ActorKind != "human" || !e.OK || e.Resource != "auth") {
			t.Errorf("login row = %+v (ADR-043: kind, ok, resource)", e)
		}
	}
	for _, a := range []string{"user.create", "auth.login", "auth.logout"} {
		if !actions[a] {
			t.Errorf("missing audit %q in %v", a, actions)
		}
	}
}

func TestLoginFailures(t *testing.T) {
	svc, _, c := newService(t)
	ctx := context.Background()
	u, _ := svc.CreateUser(ctx, auth.NewUser{Email: "m@x.io", Role: storage.RoleMember, Password: "member-password"}, "system")

	if _, err := svc.Login(ctx, "m@x.io", "wrong-password", auth.ClientMeta{IP: "2.2.2.2"}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if _, err := svc.Login(ctx, "ghost@x.io", "whatever-pass", auth.ClientMeta{IP: "2.2.2.2"}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("unknown user err = %v", err)
	}
	// throttle after repeated failures for the same email
	for i := 0; i < 5; i++ {
		svc.Login(ctx, "m@x.io", "wrong-password", auth.ClientMeta{IP: "3.3.3.3"})
	}
	if _, err := svc.Login(ctx, "m@x.io", "member-password", auth.ClientMeta{IP: "3.3.3.3"}); !errors.Is(err, auth.ErrThrottled) {
		t.Fatalf("expected throttle, got %v", err)
	}
	c.Advance(16 * time.Minute)
	if _, err := svc.Login(ctx, "m@x.io", "member-password", auth.ClientMeta{IP: "3.3.3.3"}); err != nil {
		t.Fatalf("after lockout window: %v", err)
	}
	// disabled user cannot log in and existing sessions stop working
	res, _ := svc.Login(ctx, "m@x.io", "member-password", auth.ClientMeta{})
	if err := svc.SetDisabled(ctx, u.ID, true, "system"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("disabled session err = %v", err)
	}
	if _, err := svc.Login(ctx, "m@x.io", "member-password", auth.ClientMeta{}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("disabled login err = %v", err)
	}
}

// One IP guessing across many emails is locked out, other IPs are not.
func TestLoginIPThrottle(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	svc.CreateUser(ctx, auth.NewUser{Email: "ok@x.io", Role: storage.RoleMember, Password: "the-password"}, "system")
	for i := 0; i < 20; i++ {
		svc.Login(ctx, "guess"+strings.Repeat("x", i)+"@x.io", "nope-nope-nope", auth.ClientMeta{IP: "6.6.6.6"})
	}
	if _, err := svc.Login(ctx, "ok@x.io", "the-password", auth.ClientMeta{IP: "6.6.6.6"}); !errors.Is(err, auth.ErrThrottled) {
		t.Fatalf("same IP err = %v", err)
	}
	if _, err := svc.Login(ctx, "ok@x.io", "the-password", auth.ClientMeta{IP: "7.7.7.7"}); err != nil {
		t.Fatalf("other IP err = %v", err)
	}
}

// blockingHasher holds every Verify until release is closed.
type blockingHasher struct {
	auth.Hasher
	started chan struct{}
	release chan struct{}
}

func (h blockingHasher) Verify(enc, pw string) (bool, error) {
	h.started <- struct{}{}
	<-h.release
	return h.Hasher.Verify(enc, pw)
}

// Password checks beyond MaxHashing wait, then give up with ErrBusy.
func TestLoginHashingBounded(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := blockingHasher{Hasher: auth.FastHasherForTests(), started: make(chan struct{}, 4), release: make(chan struct{})}
	svc := auth.NewService(st, auth.Options{Hasher: h, MaxHashing: 1, HashWait: 50 * time.Millisecond})
	ctx := context.Background()
	go svc.Login(ctx, "a@x.io", "whatever-pass", auth.ClientMeta{IP: "1.1.1.1"})
	<-h.started
	if _, err := svc.Login(ctx, "b@x.io", "whatever-pass", auth.ClientMeta{IP: "1.1.1.2"}); !errors.Is(err, auth.ErrBusy) {
		t.Fatalf("second login err = %v", err)
	}
	close(h.release)
}

func TestSessionExpiry(t *testing.T) {
	svc, _, c := newService(t)
	ctx := context.Background()
	svc.CreateUser(ctx, auth.NewUser{Email: "e@x.io", Role: storage.RoleMember, Password: "expiry-password"}, "system")
	res, _ := svc.Login(ctx, "e@x.io", "expiry-password", auth.ClientMeta{})

	c.Advance(23 * time.Hour) // within idle timeout; Authenticate refreshes last_seen
	if _, _, err := svc.Authenticate(ctx, res.Token); err != nil {
		t.Fatalf("within idle: %v", err)
	}
	c.Advance(25 * time.Hour) // idle > 24h
	if _, _, err := svc.Authenticate(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("idle expired err = %v", err)
	}

	res, _ = svc.Login(ctx, "e@x.io", "expiry-password", auth.ClientMeta{})
	for i := 0; i < 8; i++ { // keep active but pass absolute TTL (7d)
		c.Advance(23 * time.Hour)
		svc.Authenticate(ctx, res.Token)
	}
	if _, _, err := svc.Authenticate(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("absolute TTL err = %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, "garbage"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("garbage token err = %v", err)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	u, _ := svc.CreateUser(ctx, auth.NewUser{Email: "p@x.io", Role: storage.RoleMember, Password: "old-password-1"}, "system")
	a, _ := svc.Login(ctx, "p@x.io", "old-password-1", auth.ClientMeta{})
	b, _ := svc.Login(ctx, "p@x.io", "old-password-1", auth.ClientMeta{})

	if err := svc.ChangePassword(ctx, u.ID, "wrong-old-pass", "new-password-2", a.Session.ID); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong old err = %v", err)
	}
	if err := svc.ChangePassword(ctx, u.ID, "old-password-1", "new-password-2", a.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, a.Token); err != nil {
		t.Fatalf("current session should survive: %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, b.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("other session should be revoked: %v", err)
	}
	if _, err := svc.Login(ctx, "p@x.io", "new-password-2", auth.ClientMeta{}); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}
