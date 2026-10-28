package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/tung1998/agent-office/internal/storage"
)

// Passkeys (ADR-226): a way to sign in besides the password. Browsers allow
// WebAuthn only on HTTPS or localhost, and the relying party id must be a
// host name, so an office opened as http://<ip>:port keeps the password only.

var (
	ErrPasskeyUnavailable = errors.New("auth: passkeys need HTTPS with a host name, or localhost")
	ErrPasskeyFailed      = errors.New("auth: passkey not accepted")
)

// passkeyStateTTL is how long a started registration or login waits.
const passkeyStateTTL = 5 * time.Minute

// passkeyStates keeps the challenges of ceremonies in progress, in memory:
// a restart in between only means trying again.
type passkeyStates struct {
	mu sync.Mutex
	m  map[string]passkeyState
}

type passkeyState struct {
	data    webauthn.SessionData
	userID  string // "" for a login
	origin  string
	expires time.Time
}

func (p *passkeyStates) put(st passkeyState, now time.Time) (string, error) {
	id, _, err := newToken()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]passkeyState{}
	}
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
	st.expires = now.Add(passkeyStateTTL)
	p.m[id] = st
	return id, nil
}

// take returns a state once: a challenge is never answered twice.
func (p *passkeyStates) take(id string, now time.Time) (passkeyState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.m[id]
	delete(p.m, id)
	return st, ok && now.Before(st.expires)
}

// relyingParty is the WebAuthn config for the origin the browser is on.
func relyingParty(origin string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return nil, ErrPasskeyUnavailable
	}
	host := u.Hostname()
	local := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if (u.Scheme != "https" && !(u.Scheme == "http" && local)) || net.ParseIP(host) != nil {
		return nil, ErrPasskeyUnavailable
	}
	return webauthn.New(&webauthn.Config{
		RPID: host, RPDisplayName: "agent-office", RPOrigins: []string{u.Scheme + "://" + u.Host},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired,
		},
	})
}

// PasskeyAvailable reports whether passkeys work on origin.
func PasskeyAvailable(origin string) bool {
	_, err := relyingParty(origin)
	return err == nil
}

// passkeyUser is an account as the WebAuthn library sees it.
type passkeyUser struct {
	u     storage.User
	keys  []storage.Passkey
	creds []webauthn.Credential
}

func (p passkeyUser) WebAuthnID() []byte                         { return []byte(p.u.ID) }
func (p passkeyUser) WebAuthnName() string                       { return p.u.Email }
func (p passkeyUser) WebAuthnDisplayName() string                { return firstNonEmpty(p.u.Name, p.u.Email) }
func (p passkeyUser) WebAuthnCredentials() []webauthn.Credential { return p.creds }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) passkeyUser(ctx context.Context, userID string) (passkeyUser, error) {
	u, err := s.store.Users().GetByID(ctx, userID)
	if err != nil {
		return passkeyUser{}, err
	}
	keys, err := s.store.Passkeys().List(ctx, u.ID)
	if err != nil {
		return passkeyUser{}, err
	}
	pu := passkeyUser{u: u, keys: keys}
	for _, k := range keys {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(k.Credential), &c); err != nil {
			return passkeyUser{}, err
		}
		pu.creds = append(pu.creds, c)
	}
	return pu, nil
}

// BeginPasskeyRegistration starts adding a passkey to the account: options
// go to navigator.credentials.create, state comes back with the answer.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, userID, origin string) (options any, state string, err error) {
	rp, err := relyingParty(origin)
	if err != nil {
		return nil, "", err
	}
	pu, err := s.passkeyUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(pu.creds))
	for _, c := range pu.creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts, data, err := rp.BeginRegistration(pu, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, "", err
	}
	state, err = s.passkeys.put(passkeyState{data: *data, userID: userID, origin: origin}, s.opts.Now())
	return opts, state, err
}

// FinishPasskeyRegistration checks the browser's answer and keeps the passkey.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, userID, origin, state, name string, response []byte) (storage.Passkey, error) {
	st, ok := s.passkeys.take(state, s.opts.Now())
	if !ok || st.userID != userID || st.origin != origin {
		return storage.Passkey{}, ErrPasskeyFailed
	}
	rp, err := relyingParty(origin)
	if err != nil {
		return storage.Passkey{}, err
	}
	pu, err := s.passkeyUser(ctx, userID)
	if err != nil {
		return storage.Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return storage.Passkey{}, ErrPasskeyFailed
	}
	cred, err := rp.CreateCredential(pu, st.data, parsed)
	if err != nil {
		return storage.Passkey{}, ErrPasskeyFailed
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return storage.Passkey{}, err
	}
	if name = truncate(strings.TrimSpace(name), 100); name == "" {
		name = "Passkey"
	}
	p, err := s.store.Passkeys().Create(ctx, storage.Passkey{UserID: userID, Name: name,
		CredentialID: base64.RawURLEncoding.EncodeToString(cred.ID), Credential: string(raw)})
	if errors.Is(err, storage.ErrConflict) {
		return storage.Passkey{}, ErrPasskeyFailed
	}
	if err != nil {
		return storage.Passkey{}, err
	}
	s.audit(ctx, "human:"+pu.u.Email, "user.passkey_add", userID, map[string]any{"passkey": p.ID, "name": p.Name})
	return p, nil
}

// ListPasskeys lists an account's passkeys.
func (s *Service) ListPasskeys(ctx context.Context, userID string) ([]storage.Passkey, error) {
	return s.store.Passkeys().List(ctx, userID)
}

// DeletePasskey removes one of the account's passkeys.
func (s *Service) DeletePasskey(ctx context.Context, userID, id, actor string) error {
	if err := s.store.Passkeys().Delete(ctx, id, userID); err != nil {
		return err
	}
	s.audit(ctx, actor, "user.passkey_delete", userID, map[string]any{"passkey": id})
	return nil
}

// BeginPasskeyLogin starts a login with any passkey of this office (no email
// typed): options go to navigator.credentials.get.
func (s *Service) BeginPasskeyLogin(ctx context.Context, origin string, meta ClientMeta) (options any, state string, err error) {
	if meta.IP != "" && s.ipThrottle.locked(meta.IP, s.opts.Now()) {
		return nil, "", ErrThrottled
	}
	rp, err := relyingParty(origin)
	if err != nil {
		return nil, "", err
	}
	opts, data, err := rp.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", err
	}
	state, err = s.passkeys.put(passkeyState{data: *data, origin: origin}, s.opts.Now())
	return opts, state, err
}

// FinishPasskeyLogin checks the signed challenge and opens a session.
func (s *Service) FinishPasskeyLogin(ctx context.Context, origin, state string, response []byte, meta ClientMeta) (LoginResult, error) {
	now := s.opts.Now()
	if meta.IP != "" && s.ipThrottle.locked(meta.IP, now) {
		return LoginResult{}, ErrThrottled
	}
	st, ok := s.passkeys.take(state, now)
	if !ok || st.userID != "" || st.origin != origin {
		return LoginResult{}, s.passkeyFail(ctx, meta, "", "stale_challenge")
	}
	rp, err := relyingParty(origin)
	if err != nil {
		return LoginResult{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return LoginResult{}, s.passkeyFail(ctx, meta, "", "bad_response")
	}
	var pu passkeyUser
	_, cred, err := rp.ValidatePasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		k, err := s.store.Passkeys().GetByCredentialID(ctx, base64.RawURLEncoding.EncodeToString(rawID))
		if err != nil {
			return nil, err
		}
		if k.UserID != string(userHandle) {
			return nil, ErrPasskeyFailed
		}
		if pu, err = s.passkeyUser(ctx, k.UserID); err != nil {
			return nil, err
		}
		return pu, nil
	}, st.data, parsed)
	if err != nil {
		return LoginResult{}, s.passkeyFail(ctx, meta, pu.u.Email, "bad_passkey")
	}
	if pu.u.Disabled {
		return LoginResult{}, s.passkeyFail(ctx, meta, pu.u.Email, "disabled")
	}
	if cred.Authenticator.CloneWarning {
		return LoginResult{}, s.passkeyFail(ctx, meta, pu.u.Email, "clone_warning")
	}
	for _, k := range pu.keys {
		if k.CredentialID == base64.RawURLEncoding.EncodeToString(cred.ID) {
			if raw, err := json.Marshal(cred); err == nil {
				_ = s.store.Passkeys().Used(ctx, k.ID, string(raw), now)
			}
		}
	}
	return s.openSession(ctx, pu.u, meta, map[string]any{"method": "passkey"})
}

func (s *Service) passkeyFail(ctx context.Context, meta ClientMeta, email, reason string) error {
	if meta.IP != "" {
		s.ipThrottle.fail(meta.IP, s.opts.Now())
	}
	s.audit(ctx, "anonymous", "auth.login_failed", email, map[string]any{"ip": meta.IP, "reason": reason, "method": "passkey"})
	return ErrPasskeyFailed
}
