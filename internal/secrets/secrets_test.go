package secrets_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/secrets"
)

func TestSealOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	box, err := secrets.Load(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal("sk-ant-very-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "very-secret") || !strings.HasPrefix(enc, "v1:") {
		t.Fatalf("ciphertext leaks or wrong format: %q", enc)
	}
	enc2, _ := box.Seal("sk-ant-very-secret")
	if enc == enc2 {
		t.Fatal("nonce must be random")
	}
	got, err := box.Open(enc)
	if err != nil || got != "sk-ant-very-secret" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "secret.key"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v", info.Mode().Perm())
	}
	// reloading uses the same key
	box2, _ := secrets.Load(filepath.Join(dir, "secret.key"))
	if got, err := box2.Open(enc); err != nil || got != "sk-ant-very-secret" {
		t.Fatalf("reload Open = %q, %v", got, err)
	}
	// a different key cannot open it
	other, _ := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	if _, err := other.Open(enc); err == nil {
		t.Fatal("foreign key opened ciphertext")
	}
	if _, err := box.Open("v1:garbage"); err == nil {
		t.Fatal("garbage opened")
	}
	if got, err := box.Open(""); err != nil || got != "" {
		t.Fatalf("empty = %q, %v", got, err)
	}
}

func TestHint(t *testing.T) {
	if h := secrets.Hint("sk-ant-api03-abcdWXYZ"); h != "…WXYZ" {
		t.Fatalf("hint = %q", h)
	}
	if h := secrets.Hint("abc"); h != "…" {
		t.Fatalf("short hint = %q", h)
	}
}
