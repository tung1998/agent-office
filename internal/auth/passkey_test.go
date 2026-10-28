package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
)

const origin = "https://office.example.ts.net"

// softKey is a passkey held in memory: it answers the challenges a browser
// would pass to a real authenticator.
type softKey struct {
	t      *testing.T
	priv   *ecdsa.PrivateKey
	id     []byte
	user   []byte // the user handle given at registration
	rpID   string
	origin string
	count  uint32
}

func newSoftKey(t *testing.T) *softKey {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{t: t, priv: priv, id: id, rpID: "office.example.ts.net", origin: origin}
}

var b64 = base64.RawURLEncoding

func publicKey(t *testing.T, options any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		PublicKey map[string]any `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	return o.PublicKey
}

func (k *softKey) clientData(typ string, options any) []byte {
	cd, _ := json.Marshal(map[string]any{"type": typ, "challenge": publicKey(k.t, options)["challenge"], "origin": k.origin})
	return cd
}

func (k *softKey) authData(flags byte, attested []byte) []byte {
	h := sha256.Sum256([]byte(k.rpID))
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, k.count)
	return append(out, attested...)
}

// create answers navigator.credentials.create.
func (k *softKey) create(options any) []byte {
	user, _ := publicKey(k.t, options)["user"].(map[string]any)
	k.user, _ = b64.DecodeString(user["id"].(string))
	cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: k.priv.X.FillBytes(make([]byte, 32)), -3: k.priv.Y.FillBytes(make([]byte, 32))})
	attested := append(make([]byte, 16), byte(len(k.id)>>8), byte(len(k.id)))
	attested = append(append(attested, k.id...), cose...)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(0x45, attested)})
	out, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(k.clientData("webauthn.create", options)), "attestationObject": b64.EncodeToString(att)}})
	return out
}

// get answers navigator.credentials.get.
func (k *softKey) get(options any) []byte {
	k.count++
	ad := k.authData(0x05, nil)
	cd := k.clientData("webauthn.get", options)
	h := sha256.Sum256(cd)
	sum := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, sum[:])
	if err != nil {
		k.t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(k.user)}})
	return out
}

func TestPasskeyAvailable(t *testing.T) {
	for o, want := range map[string]bool{
		"https://office.example.ts.net": true, "http://localhost:3000": true, "https://localhost": true,
		"http://100.115.62.90:2704": false, "https://100.115.62.90": false, "http://office.example": false, "": false,
	} {
		if got := auth.PasskeyAvailable(o); got != want {
			t.Errorf("PasskeyAvailable(%q) = %v, want %v", o, got, want)
		}
	}
}

func TestPasskeyRegisterAndLogin(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, auth.NewUser{Email: "a@example.com", Role: storage.RoleMember, Password: "long-enough-pw"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	key := newSoftKey(t)

	opts, state, err := svc.BeginPasskeyRegistration(ctx, u.ID, origin)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.FinishPasskeyRegistration(ctx, u.ID, origin, state, "MacBook", key.create(opts))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if p.Name != "MacBook" {
		t.Fatalf("name = %q", p.Name)
	}
	// a challenge is answered once
	if _, err := svc.FinishPasskeyRegistration(ctx, u.ID, origin, state, "again", key.create(opts)); !errors.Is(err, auth.ErrPasskeyFailed) {
		t.Fatalf("replayed registration = %v, want ErrPasskeyFailed", err)
	}

	meta := auth.ClientMeta{IP: "10.0.0.1"}
	opts, state, err = svc.BeginPasskeyLogin(ctx, origin, meta)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.FinishPasskeyLogin(ctx, origin, state, key.get(opts), meta)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.User.ID != u.ID || res.Token == "" {
		t.Fatalf("login result = %+v", res)
	}
	if _, _, err := svc.Authenticate(ctx, res.Token); err != nil {
		t.Fatalf("session not usable: %v", err)
	}
	keys, _ := st.Passkeys().List(ctx, u.ID)
	if len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("passkeys after login = %+v", keys)
	}

	// another origin's answer is refused
	opts, state, _ = svc.BeginPasskeyLogin(ctx, origin, meta)
	key.origin = "https://evil.example"
	if _, err := svc.FinishPasskeyLogin(ctx, origin, state, key.get(opts), meta); !errors.Is(err, auth.ErrPasskeyFailed) {
		t.Fatalf("wrong origin = %v, want ErrPasskeyFailed", err)
	}
	key.origin = origin

	// a disabled account cannot sign in with its passkey
	if err := svc.SetDisabled(ctx, u.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	opts, state, _ = svc.BeginPasskeyLogin(ctx, origin, meta)
	if _, err := svc.FinishPasskeyLogin(ctx, origin, state, key.get(opts), meta); !errors.Is(err, auth.ErrPasskeyFailed) {
		t.Fatalf("disabled = %v, want ErrPasskeyFailed", err)
	}

	// deleted: gone for good
	if err := svc.DeletePasskey(ctx, u.ID, p.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := svc.ListPasskeys(ctx, u.ID); len(keys) != 0 {
		t.Fatalf("passkeys after delete = %d", len(keys))
	}
}

func TestPasskeyNeedsHTTPS(t *testing.T) {
	svc, _, _ := newService(t)
	if _, _, err := svc.BeginPasskeyLogin(context.Background(), "http://100.115.62.90:2704", auth.ClientMeta{}); !errors.Is(err, auth.ErrPasskeyUnavailable) {
		t.Fatalf("plain http ip = %v, want ErrPasskeyUnavailable", err)
	}
}
