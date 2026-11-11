package auth

import (
	"context"

	"github.com/tung1998/agent-office/internal/storage"
)

// ListSessions: the account's signed-in devices, the last used first
// (ADR-272), to see where it is signed in and revoke a lost device.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]storage.Session, error) {
	list, err := s.store.Sessions().ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := s.opts.Now()
	out := list[:0]
	for _, x := range list {
		if x.ExpiresAt.After(now) { // an expired row waits for the sweep, it signs nobody in
			out = append(out, x)
		}
	}
	return out, nil
}

// RevokeSession signs one of the account's devices out at once: its next
// request gets 401. Another account's session is ErrNotFound (the check is in
// the storage's DELETE … AND user_id).
func (s *Service) RevokeSession(ctx context.Context, userID, id, actor string) error {
	if err := s.store.Sessions().DeleteOwned(ctx, userID, id); err != nil {
		return err
	}
	s.audit(ctx, actor, "user.session_revoke", userID, map[string]any{"session": id})
	return nil
}
