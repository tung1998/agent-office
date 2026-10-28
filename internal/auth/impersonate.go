package auth

import (
	"context"
	"errors"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// ViewTTL caps a session an admin opens to view the office as another account.
const ViewTTL = time.Hour

// ErrCannotView: the account is not one an admin may view as (itself, another
// admin, disabled or not set up yet).
var ErrCannotView = errors.New("auth: cannot view as this account")

// ViewAs opens a read-only session of the account targetID for admin, who
// stays logged in with its own session (the caller keeps that token to come
// back). It lasts ViewTTL, never past admin's own session.
func (s *Service) ViewAs(ctx context.Context, admin storage.User, adminSess storage.Session, targetID string, meta ClientMeta) (LoginResult, error) {
	if admin.Role != storage.RoleAdmin || adminSess.ImpersonatorID != "" {
		return LoginResult{}, ErrCannotView
	}
	u, err := s.store.Users().GetByID(ctx, targetID)
	if err != nil {
		return LoginResult{}, err
	}
	if u.ID == admin.ID || u.Role == storage.RoleAdmin || u.Disabled || u.MustChange {
		return LoginResult{}, ErrCannotView
	}
	now := s.opts.Now()
	exp := now.Add(ViewTTL)
	if adminSess.ExpiresAt.Before(exp) {
		exp = adminSess.ExpiresAt
	}
	token, hash, err := newToken()
	if err != nil {
		return LoginResult{}, err
	}
	sess, err := s.store.Sessions().Create(ctx, storage.Session{
		UserID: u.ID, TokenHash: hash, CreatedAt: now, ExpiresAt: exp, LastSeenAt: now,
		UserAgent: truncate(meta.UserAgent, 255), IP: meta.IP, ImpersonatorID: admin.ID,
	})
	if err != nil {
		return LoginResult{}, err
	}
	s.audit(ctx, "human:"+admin.Email, "auth.view_as", u.ID, map[string]any{"ip": meta.IP, "session": sess.ID, "email": u.Email})
	return LoginResult{Token: token, User: u, Session: sess}, nil
}

// EndView closes the viewing session viewToken and returns the admin's own
// session behind backToken, when it is still valid and is the one that
// opened the view.
func (s *Service) EndView(ctx context.Context, viewToken, backToken string) (storage.User, error) {
	_, view, err := s.Authenticate(ctx, viewToken)
	if err == nil && view.ImpersonatorID == "" {
		return storage.User{}, ErrUnauthenticated // a normal login: nothing to end
	}
	if err == nil {
		_ = s.store.Sessions().Delete(ctx, view.ID)
		s.audit(ctx, "user:"+view.ImpersonatorID, "auth.view_as_end", view.UserID, map[string]any{"session": view.ID})
	}
	admin, sess, err := s.Authenticate(ctx, backToken)
	if err != nil {
		return storage.User{}, err
	}
	if sess.ImpersonatorID != "" || admin.Role != storage.RoleAdmin || (view.ImpersonatorID != "" && view.ImpersonatorID != admin.ID) {
		return storage.User{}, ErrUnauthenticated
	}
	return admin, nil
}
