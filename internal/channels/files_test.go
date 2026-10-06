package channels

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
