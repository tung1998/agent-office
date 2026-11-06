package clitools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func registry(t *testing.T, versions map[string]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for pkg, v := range versions {
			if strings.Contains(r.URL.Path, strings.ReplaceAll(pkg, "/", "%2F")) || strings.Contains(r.URL.Path, pkg) {
				_, _ = w.Write([]byte(`{"version":"` + v + `"}`))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type memSetting struct {
	s    AutoSetting
	told []string
}

func (x *memSetting) hooks() WatchHooks {
	return WatchHooks{
		Load:   func(context.Context) AutoSetting { return x.s },
		Save:   func(_ context.Context, s AutoSetting) { x.s = s },
		Notify: func(_ context.Context, text string) { x.told = append(x.told, text) },
	}
}

// A new version is told once; the next look (or a restart, the setting
// keeps it) does not tell it again; a newer one is told.
func TestWatchNotifiesOnce(t *testing.T) {
	m, dir := testManager(t)
	reg := map[string]string{"@google/gemini-cli": "0.67.0"}
	m.registry = registry(t, reg).URL
	npmTool(t, dir, "gemini", "@google/gemini-cli", "0.66.0")
	x := &memSetting{}
	ctx := context.Background()
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 1 || !strings.Contains(x.told[0], "0.67.0") || !strings.Contains(x.told[0], "/update cli gemini") || x.s.Told["gemini"] != "0.67.0" {
		t.Fatalf("told = %q, setting %+v", x.told, x.s)
	}
	m.forgetLatest("gemini")
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 1 {
		t.Fatalf("told twice: %q", x.told)
	}
	reg["@google/gemini-cli"] = "0.68.0"
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 2 || !strings.Contains(x.told[1], "0.68.0") {
		t.Fatalf("a newer version was not told: %q", x.told)
	}
	if u, ok := m.Upgrade("gemini"); ok {
		t.Fatalf("notify mode updated: %+v", u)
	}
	// off: nothing
	x.s.Mode = AutoOff
	reg["@google/gemini-cli"] = "0.69.0"
	m.forgetLatest("gemini")
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 2 {
		t.Fatalf("off told: %q", x.told)
	}
}

// Auto mode updates when idle; a CLI of its own mode overrides the default.
func TestWatchAutoUpdates(t *testing.T) {
	m, dir := testManager(t)
	m.registry = registry(t, map[string]string{"@openai/codex": "0.167.0", "@google/gemini-cli": "0.67.0"}).URL
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	npmTool(t, dir, "gemini", "@google/gemini-cli", "0.66.0")
	writeBin(t, dir, "npm", "#!/bin/sh\nprintf '#!/bin/sh\\necho codex-cli 0.167.0\\n' > \""+real+"\"\n")
	x := &memSetting{s: AutoSetting{Mode: AutoUpdate, Tools: map[string]string{"gemini": AutoOff}}}
	m.WatchOnce(context.Background(), x.hooks())
	if len(x.told) != 1 || !strings.Contains(x.told[0], "office cập nhật khi không còn lượt dùng") {
		t.Fatalf("told = %q", x.told)
	}
	v := waitUpgrade(t, m, "codex", "done", "failed", "rolled_back")
	if v.State != "done" || v.By != AutoBy || v.To != "0.167.0" {
		t.Fatalf("auto update = %+v", v)
	}
	if _, ok := m.Upgrade("gemini"); ok {
		t.Fatal("gemini is off")
	}
}

// A CLI broken outside office (npm cut short: its binary gone) is told
// once; once it starts again, a new break is told again.
func TestWatchTellsBrokenInstall(t *testing.T) {
	m, dir := testManager(t)
	m.registry = registry(t, map[string]string{"@openai/codex": "0.166.0"}).URL
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	x := &memSetting{}
	ctx := context.Background()
	broken := "#!/bin/sh\necho 'Error: spawn /x/vendor/codex ENOENT' >&2\nexit 1\n"
	if err := os.WriteFile(real, []byte(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	m.WatchOnce(ctx, x.hooks())
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 1 || !strings.Contains(x.told[0], "không chạy được") || x.s.Told["codex"] != "broken" {
		t.Fatalf("told = %q %+v", x.told, x.s)
	}
	npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	m.WatchOnce(ctx, x.hooks())
	if _, ok := x.s.Told["codex"]; ok {
		t.Fatalf("the broken mark stayed: %+v", x.s)
	}
	_ = os.WriteFile(real, []byte(broken), 0o755)
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 2 {
		t.Fatalf("a second break was not told: %q", x.told)
	}
}

func TestModeOf(t *testing.T) {
	s := AutoSetting{Mode: "bogus", Tools: map[string]string{"codex": AutoUpdate}}
	if s.ModeOf("claude") != AutoNotify || s.ModeOf("codex") != AutoUpdate || CleanMode("stop") != "" {
		t.Fatal("modes")
	}
}

// A version told under notify is updated once the mode turns auto; a version
// whose update was put back is not tried again on its own.
func TestWatchAutoAfterNotify(t *testing.T) {
	m, dir := testManager(t)
	m.registry = registry(t, map[string]string{"@openai/codex": "0.167.0"}).URL
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", "#!/bin/sh\nprintf '#!/bin/sh\\necho codex-cli 0.167.0\\n' > \""+real+"\"\n")
	x := &memSetting{}
	ctx := context.Background()
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 1 {
		t.Fatalf("told = %q", x.told)
	}
	x.s.Mode = AutoUpdate
	m.forgetLatest("codex")
	m.WatchOnce(ctx, x.hooks())
	if len(x.told) != 1 {
		t.Fatalf("told again: %q", x.told)
	}
	v := waitUpgrade(t, m, "codex", "done", "failed", "rolled_back")
	if v.State != "done" || v.To != "0.167.0" {
		t.Fatalf("not updated after notify -> auto: %+v", v)
	}

	// a bad version: not updated to again on its own
	m2, dir2 := testManager(t)
	m2.registry = m.registry
	npmTool(t, dir2, "codex", "@openai/codex", "0.166.0")
	m2.bad = map[string]string{"codex": "0.167.0"}
	y := &memSetting{s: AutoSetting{Mode: AutoUpdate}}
	m2.WatchOnce(ctx, y.hooks())
	if _, ok := m2.Upgrade("codex"); ok || len(y.told) != 1 {
		t.Fatalf("a put-back version was updated to again; told %q", y.told)
	}
}
