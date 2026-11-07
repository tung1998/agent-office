package channels

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// A file goes up as multipart: on Discord with its caption, on Telegram an
// image as a photo and anything else as a document.
func TestSendFile(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
		}
		var file string
		for field, fs := range r.MultipartForm.File {
			f, _ := fs[0].Open()
			b, _ := io.ReadAll(f)
			file = field + "=" + fs[0].Filename + ":" + string(b)
		}
		got = append(got, r.URL.Path+" "+r.Header.Get("Authorization")+" "+file+" "+r.FormValue("payload_json")+r.FormValue("caption")+r.FormValue("chat_id"))
		if strings.Contains(r.URL.Path, "/bot") {
			w.Write([]byte(`{"ok":true,"result":{"message_id":9}}`))
			return
		}
		w.Write([]byte(`{"id":"m1"}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	d := &Discord{Token: "TOK", APIBase: srv.URL}
	if id, err := d.SendFile(ctx, "c2", "chụp.png", []byte("PNG"), "trang chủ"); err != nil || id != "m1" {
		t.Fatal(id, err)
	}
	tg := &Telegram{Token: "T", BaseURL: srv.URL}
	if id, err := tg.SendFile(ctx, "42", "a.png", []byte("PNG"), "ảnh"); err != nil || id != "9" {
		t.Fatal(id, err)
	}
	tg.SendFile(ctx, "42", "log.txt", []byte("dòng 1"), "")
	all := strings.Join(got, "\n")
	for _, want := range []string{"/channels/c2/messages Bot TOK files[0]=chụp.png:PNG", `"content":"trang chủ"`,
		"/botT/sendPhoto  photo=a.png:PNG", "ảnh42", "/botT/sendDocument  document=log.txt:dòng 1"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in:\n%s", want, all)
		}
	}
}

// allowedFile must refuse common credential filenames, not just .env/.pem/.key.
func TestAllowedFileBlocksCredentialFiles(t *testing.T) {
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st}
	ctx := context.Background()
	sc := actions.Scope{Dir: tmp}

	for _, name := range []string{
		"id_rsa", "id_rsa.pub", "id_ed25519",
		"credentials.json", ".npmrc", ".netrc",
		"cert.p12", "cert.pfx", "keys.keystore",
	} {
		path := filepath.Join(tmp, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.allowedFile(ctx, sc, name); err == nil {
			t.Errorf("allowedFile(%q): want error, got nil", name)
		}
	}

	awsDir := filepath.Join(tmp, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(awsDir, "credentials"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.allowedFile(ctx, sc, ".aws/credentials"); err == nil {
		t.Errorf("allowedFile(.aws/credentials): want error, got nil")
	}

	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.allowedFile(ctx, sc, ".ssh/id_rsa"); err == nil {
		t.Errorf("allowedFile(.ssh/id_rsa): want error, got nil")
	}
}
