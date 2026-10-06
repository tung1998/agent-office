// Package auth manages dashboard accounts and login sessions.
//
// Passwords are argon2id. A session token is 32 random bytes given to the
// browser in an HttpOnly cookie; only its SHA-256 is stored, so a leaked
// database cannot be replayed as a login.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	actorpkg "github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/audit"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/storage"
)

var (
	ErrInvalidCredentials = errors.New("auth: invalid email or password")
	ErrThrottled          = errors.New("auth: too many failed attempts, try again later")
	ErrBusy               = errors.New("auth: too many logins at once, try again")
	ErrUnauthenticated    = errors.New("auth: not logged in")
	ErrInvalidEmail       = errors.New("auth: invalid email")
	ErrWeakPassword       = fmt.Errorf("auth: password must be at least %d characters", MinPasswordLen)
	ErrInvalidRole        = errors.New("auth: invalid role")
	ErrEmailTaken         = errors.New("auth: email already registered")
	ErrSetUp              = errors.New("auth: account already set up")
)

// The account a first run makes (EnsureDefaultAdmin) when setup did not pick
// one. It must set a real email and password before anything else
// (SetupAccount), so admin / admin lives only until the first login.
const (
	DefaultAdminEmail    = "admin"
	DefaultAdminPassword = "admin"
)

// MinPasswordLen is the minimum password length (NIST 800-63B favours length).
const MinPasswordLen = 10

// Options tune the service. Zero values get safe defaults.
type Options struct {
	SessionTTL    time.Duration // absolute lifetime, default 7 days
	IdleTimeout   time.Duration // max gap between requests, default 24h
	MaxFailures   int           // per email before lockout, default 5
	MaxIPFailures int           // per client IP (any emails) before lockout, default 20
	FailureWindow time.Duration // lockout window, default 15m
	// MaxHashing bounds password checks running at once (argon2id takes 64 MiB
	// each), default 2: a flood of logins waits instead of exhausting memory.
	MaxHashing int
	HashWait   time.Duration // how long a login waits for its turn, default 10s
	Now        func() time.Time
	Hasher     Hasher
}

// Service is the auth use-case layer used by the API and the CLI.
type Service struct {
	store      storage.Store
	opts       Options
	throttle   *throttle // by email
	ipThrottle *throttle // by client IP: guessing across many emails
	hashing    chan struct{}
	dummy      string // hash verified for unknown emails so timing does not leak existence
}

// NewService builds a Service.
func NewService(store storage.Store, opts Options) *Service {
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 7 * 24 * time.Hour
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 24 * time.Hour
	}
	if opts.MaxFailures == 0 {
		opts.MaxFailures = 5
	}
	if opts.MaxIPFailures == 0 {
		opts.MaxIPFailures = 20
	}
	if opts.FailureWindow == 0 {
		opts.FailureWindow = 15 * time.Minute
	}
	if opts.MaxHashing == 0 {
		opts.MaxHashing = 2
	}
	if opts.HashWait == 0 {
		opts.HashWait = 10 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Hasher == nil {
		opts.Hasher = DefaultHasher()
	}
	dummy, _ := opts.Hasher.Hash("dummy-password-for-timing")
	return &Service{store: store, opts: opts, throttle: newThrottle(opts.MaxFailures, opts.FailureWindow),
		ipThrottle: newThrottle(opts.MaxIPFailures, opts.FailureWindow), hashing: make(chan struct{}, opts.MaxHashing), dummy: dummy}
}

// verify checks a password once a hashing slot is free (ErrBusy after HashWait).
func (s *Service) verify(ctx context.Context, encoded, password string) (bool, error) {
	t := time.NewTimer(s.opts.HashWait)
	defer t.Stop()
	select {
	case s.hashing <- struct{}{}:
	case <-t.C:
		return false, ErrBusy
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-s.hashing }()
	return s.opts.Hasher.Verify(encoded, password)
}

// SessionTTL exposes the absolute lifetime (for the cookie Max-Age).
func (s *Service) SessionTTL() time.Duration { return s.opts.SessionTTL }

// NewUser is the input to CreateUser.
type NewUser struct {
	Email    string
	Name     string
	Role     storage.Role
	Password string
}

// ClientMeta describes the browser making a login.
type ClientMeta struct {
	IP        string
	UserAgent string
}

// LoginResult is returned by a successful Login.
type LoginResult struct {
	Token   string
	User    storage.User
	Session storage.Session
}

// NormalizeEmail trims and lowercases an email address.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validEmail(email string) bool {
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email && strings.Contains(email[strings.LastIndex(email, "@")+1:], ".")
}

// ValidatePassword enforces the password policy.
func ValidatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// CreateUser registers an account. actor is recorded in the audit log.
func (s *Service) CreateUser(ctx context.Context, in NewUser, actor string) (storage.User, error) {
	email := NormalizeEmail(in.Email)
	if !validEmail(email) {
		return storage.User{}, ErrInvalidEmail
	}
	if !in.Role.Valid() {
		return storage.User{}, ErrInvalidRole
	}
	if err := ValidatePassword(in.Password); err != nil {
		return storage.User{}, err
	}
	hash, err := s.opts.Hasher.Hash(in.Password)
	if err != nil {
		return storage.User{}, err
	}
	u, err := s.store.Users().Create(ctx, storage.User{Email: email, Name: strings.TrimSpace(in.Name), Role: in.Role, PasswordHash: hash})
	if errors.Is(err, storage.ErrConflict) {
		return storage.User{}, ErrEmailTaken
	}
	if err != nil {
		return storage.User{}, err
	}
	s.audit(ctx, actor, "user.create", u.ID, map[string]any{"email": u.Email, "role": string(u.Role)})
	return u, nil
}

// EnsureDefaultAdmin makes the default admin (admin / admin) when the office
// has no account yet; created is false when there was one already.
func (s *Service) EnsureDefaultAdmin(ctx context.Context) (created bool, err error) {
	n, err := s.store.Users().Count(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	hash, err := s.opts.Hasher.Hash(DefaultAdminPassword)
	if err != nil {
		return false, err
	}
	u, err := s.store.Users().Create(ctx, storage.User{Email: DefaultAdminEmail, Name: "Admin", Role: storage.RoleAdmin, PasswordHash: hash, MustChange: true})
	if errors.Is(err, storage.ErrConflict) {
		return false, nil // made at the same moment by another process
	}
	if err != nil {
		return false, err
	}
	s.audit(ctx, "system", "user.create_default", u.ID, map[string]any{"email": u.Email})
	return true, nil
}

// PendingDefault returns the default admin while it has not been set up yet.
func (s *Service) PendingDefault(ctx context.Context) (storage.User, bool) {
	u, err := s.store.Users().GetByEmail(ctx, DefaultAdminEmail)
	return u, err == nil && u.MustChange
}

// SetupAccount is the default admin setting its own email, name and password.
// Its other sessions end; keepSessionID (the one doing it) stays.
func (s *Service) SetupAccount(ctx context.Context, userID, email, name, password, keepSessionID string) (storage.User, error) {
	u, err := s.store.Users().GetByID(ctx, userID)
	if err != nil {
		return storage.User{}, err
	}
	if !u.MustChange {
		return storage.User{}, ErrSetUp
	}
	email = NormalizeEmail(email)
	if !validEmail(email) {
		return storage.User{}, ErrInvalidEmail
	}
	if err := ValidatePassword(password); err != nil {
		return storage.User{}, err
	}
	hash, err := s.opts.Hasher.Hash(password)
	if err != nil {
		return storage.User{}, err
	}
	if name = strings.TrimSpace(name); name == "" {
		name = u.Name
	}
	err = s.store.Users().SetCredentials(ctx, u.ID, email, name, hash)
	if errors.Is(err, storage.ErrConflict) {
		return storage.User{}, ErrEmailTaken
	}
	if err != nil {
		return storage.User{}, err
	}
	if err := s.store.Sessions().DeleteForUserExcept(ctx, u.ID, keepSessionID); err != nil {
		return storage.User{}, err
	}
	s.audit(ctx, "human:"+email, "user.setup", u.ID, map[string]any{"email": email})
	return s.store.Users().GetByID(ctx, u.ID)
}

// Login verifies credentials and opens a session.
func (s *Service) Login(ctx context.Context, email, password string, meta ClientMeta) (LoginResult, error) {
	email = NormalizeEmail(email)
	now := s.opts.Now()
	if s.throttle.locked(email, now) || (meta.IP != "" && s.ipThrottle.locked(meta.IP, now)) {
		s.audit(ctx, "anonymous", "auth.login_throttled", email, map[string]any{"ip": meta.IP})
		return LoginResult{}, ErrThrottled
	}
	u, err := s.store.Users().GetByEmail(ctx, email)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		if _, err := s.verify(ctx, s.dummy, password); err != nil && !errors.Is(err, errBadHash) { // equalise timing
			return LoginResult{}, err
		}
		s.fail(ctx, email, meta, "unknown_email")
		return LoginResult{}, ErrInvalidCredentials
	case err != nil:
		return LoginResult{}, err
	}
	ok, err := s.verify(ctx, u.PasswordHash, password)
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		s.fail(ctx, email, meta, "bad_password")
		return LoginResult{}, ErrInvalidCredentials
	}
	if u.Disabled {
		s.fail(ctx, email, meta, "disabled")
		return LoginResult{}, ErrInvalidCredentials
	}
	s.throttle.reset(email)

	token, hash, err := newToken()
	if err != nil {
		return LoginResult{}, err
	}
	sess, err := s.store.Sessions().Create(ctx, storage.Session{
		UserID: u.ID, TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(s.opts.SessionTTL), LastSeenAt: now,
		UserAgent: truncate(meta.UserAgent, 255), IP: meta.IP,
	})
	if err != nil {
		return LoginResult{}, err
	}
	_ = s.store.Users().TouchLogin(ctx, u.ID, now)
	s.audit(ctx, "human:"+u.Email, "auth.login", u.ID, map[string]any{"ip": meta.IP, "session": sess.ID})
	return LoginResult{Token: token, User: u, Session: sess}, nil
}

// Authenticate resolves a session token to its user.
func (s *Service) Authenticate(ctx context.Context, token string) (storage.User, storage.Session, error) {
	if token == "" {
		return storage.User{}, storage.Session{}, ErrUnauthenticated
	}
	sess, err := s.store.Sessions().GetByTokenHash(ctx, hashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return storage.User{}, storage.Session{}, ErrUnauthenticated
	}
	if err != nil {
		return storage.User{}, storage.Session{}, err
	}
	now := s.opts.Now()
	if !now.Before(sess.ExpiresAt) || now.Sub(sess.LastSeenAt) > s.opts.IdleTimeout {
		_ = s.store.Sessions().Delete(ctx, sess.ID)
		return storage.User{}, storage.Session{}, ErrUnauthenticated
	}
	u, err := s.store.Users().GetByID(ctx, sess.UserID)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && u.Disabled) {
		_ = s.store.Sessions().Delete(ctx, sess.ID)
		return storage.User{}, storage.Session{}, ErrUnauthenticated
	}
	if err != nil {
		return storage.User{}, storage.Session{}, err
	}
	// Refresh at most once a minute to avoid a write per request.
	if now.Sub(sess.LastSeenAt) > time.Minute {
		if err := s.store.Sessions().Touch(ctx, sess.ID, now); err == nil {
			sess.LastSeenAt = now
		}
	}
	return u, sess, nil
}

// Logout deletes the session behind token. Unknown tokens are not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	sess, err := s.store.Sessions().GetByTokenHash(ctx, hashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.store.Sessions().Delete(ctx, sess.ID); err != nil {
		return err
	}
	s.audit(ctx, "user:"+sess.UserID, "auth.logout", sess.UserID, map[string]any{"session": sess.ID})
	return nil
}

// ChangePassword lets a user change their own password. All sessions except
// keepSessionID are revoked.
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword, keepSessionID string) error {
	u, err := s.store.Users().GetByID(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := s.opts.Hasher.Verify(u.PasswordHash, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	if err := s.setPassword(ctx, u.ID, newPassword); err != nil {
		return err
	}
	if err := s.store.Sessions().DeleteForUserExcept(ctx, u.ID, keepSessionID); err != nil {
		return err
	}
	s.audit(ctx, "human:"+u.Email, "user.change_password", u.ID, nil)
	return nil
}

// ResetPassword sets a password without the old one (admin/CLI) and revokes all sessions.
func (s *Service) ResetPassword(ctx context.Context, userID, newPassword, actor string) error {
	if err := s.setPassword(ctx, userID, newPassword); err != nil {
		return err
	}
	if err := s.store.Sessions().DeleteForUser(ctx, userID); err != nil {
		return err
	}
	s.audit(ctx, actor, "user.reset_password", userID, nil)
	return nil
}

// SetDisabled enables or disables an account. Disabling revokes all sessions.
func (s *Service) SetDisabled(ctx context.Context, userID string, disabled bool, actor string) error {
	if err := s.store.Users().SetDisabled(ctx, userID, disabled); err != nil {
		return err
	}
	if disabled {
		if err := s.store.Sessions().DeleteForUser(ctx, userID); err != nil {
			return err
		}
	}
	s.audit(ctx, actor, "user.set_disabled", userID, map[string]any{"disabled": disabled})
	return nil
}

func (s *Service) setPassword(ctx context.Context, userID, pw string) error {
	if err := ValidatePassword(pw); err != nil {
		return err
	}
	hash, err := s.opts.Hasher.Hash(pw)
	if err != nil {
		return err
	}
	return s.store.Users().UpdatePassword(ctx, userID, hash)
}

func (s *Service) fail(ctx context.Context, email string, meta ClientMeta, reason string) {
	s.throttle.fail(email, s.opts.Now())
	if meta.IP != "" {
		s.ipThrottle.fail(meta.IP, s.opts.Now())
	}
	s.audit(ctx, "anonymous", "auth.login_failed", email, map[string]any{"ip": meta.IP, "reason": reason})
}

func (s *Service) audit(ctx context.Context, actor, action, target string, detail map[string]any) {
	c := audit.Change{Action: action, ResourceID: target, Detail: detail}
	if strings.HasSuffix(action, "_failed") || strings.HasSuffix(action, "_throttled") {
		c.Err = errors.New(strings.TrimPrefix(action, "auth."))
	}
	e := audit.Entry(actorpkg.With(ctx, actor), c)
	e.At = s.opts.Now()
	_ = s.store.Audit().Append(ctx, e)
}

func newToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
