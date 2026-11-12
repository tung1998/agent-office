package fullexport

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const pass = "correct horse battery staple"

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x", "-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// oldOffice builds an office on "the old machine": its database, files and
// one project of each kind.
func oldOffice(t *testing.T) (ExportOptions, map[string]string) {
	root := t.TempDir()
	proot := filepath.Join(root, "office-proj")
	home := filepath.Join(proot, ".office")
	paths := map[string]string{"root": proot, "home": home}

	// the office's own project: a git repo that does NOT ignore .office (its
	// database and key must still never go as code)
	run(t, root, "init", "-q", proot)
	write(t, filepath.Join(proot, "README.md"), "office\n")
	run(t, proot, "add", ".")
	run(t, proot, "commit", "-qm", "init")

	// a project with a remote: pushed, one commit not pushed, a change not
	// committed, an untracked file, an ignored one
	origin := filepath.Join(root, "origin.git")
	run(t, root, "init", "-q", "--bare", origin)
	remote := filepath.Join(root, "shop")
	run(t, root, "clone", "-q", origin, remote)
	write(t, filepath.Join(remote, "app.txt"), "v1\n")
	write(t, filepath.Join(remote, ".gitignore"), "secret.env\n")
	run(t, remote, "add", ".")
	run(t, remote, "commit", "-qm", "pushed")
	run(t, remote, "push", "-q", "origin", "HEAD:main")
	write(t, filepath.Join(remote, "app.txt"), "v2\n")
	run(t, remote, "commit", "-qam", "not pushed yet")
	write(t, filepath.Join(remote, "app.txt"), "v3 uncommitted\n")
	write(t, filepath.Join(remote, "notes/todo.md"), "untracked\n")
	write(t, filepath.Join(remote, "secret.env"), "IGNORED=1\n")
	paths["remote"] = remote

	// a local repo, no remote, with a change not committed
	local := filepath.Join(root, "tools")
	run(t, root, "init", "-q", local)
	write(t, filepath.Join(local, "a.txt"), "one\n")
	run(t, local, "add", ".")
	run(t, local, "commit", "-qm", "first")
	write(t, filepath.Join(local, "a.txt"), "two\n")
	run(t, local, "commit", "-qam", "second")
	write(t, filepath.Join(local, "a.txt"), "three dirty\n")
	paths["local"] = local

	// a folder without git
	plain := filepath.Join(root, "notes folder")
	write(t, filepath.Join(plain, "doc.md"), "plain\n")
	write(t, filepath.Join(plain, "sub/x.txt"), "x\n")
	write(t, filepath.Join(plain, "node_modules/big/index.js"), "skip me\n")
	if err := os.Symlink("doc.md", filepath.Join(plain, "link.md")); err != nil {
		t.Fatal(err)
	}
	paths["plain"] = plain

	// the office's files
	write(t, filepath.Join(home, "attachments/att_1/pic.png"), "png")
	write(t, filepath.Join(home, "accounts/prv_1/settings.json"), "{}")
	write(t, filepath.Join(home, "accounts/prv_1/.credentials.json"), "LOGIN")
	write(t, filepath.Join(home, "accounts/prv_1/session-env/s1"), "env")
	write(t, filepath.Join(home, "worktrees/w1/x"), "temp")
	write(t, filepath.Join(home, "key"), "KEY")

	// its database
	dbPath := filepath.Join(home, "office.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE repos (id TEXT PRIMARY KEY, name TEXT, path TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE sessions (id TEXT)`,
		`CREATE TABLE automations (id TEXT, enabled INTEGER, prompt TEXT)`,
		`CREATE TABLE channels (id TEXT, enabled INTEGER)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE agents (id TEXT, permissions TEXT, instructions TEXT)`,
		`CREATE TABLE providers (id TEXT, cli_home TEXT)`,
		`CREATE TABLE messages (id TEXT, content TEXT)`,
		`INSERT INTO sessions VALUES ('s1')`,
		`INSERT INTO automations VALUES ('a1', 1, 'look at ` + remote + `/app.txt')`,
		`INSERT INTO channels VALUES ('c1', 1)`,
		`INSERT INTO settings VALUES ('peers', '[{"id":"peer_1"}]'), ('theme', '"dark"')`,
		`INSERT INTO agents VALUES ('ag1', '{"extra_dirs":["` + local + `","` + local + `x"]}', '')`,
		`INSERT INTO providers VALUES ('p1', '` + home + `/accounts/prv_1')`,
		`INSERT INTO messages VALUES ('m1', 'worked in ` + remote + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()
	for i, p := range []string{proot, remote, local, plain, filepath.Join(root, "gone"), ""} {
		if _, err := sqlExec(dbPath, `INSERT INTO repos VALUES (?, ?, ?)`, string(rune('a'+i)), filepath.Base(p), p); err != nil {
			t.Fatal(err)
		}
	}

	refs := []ProjectRef{
		{ID: "a", Name: "Office", Path: proot}, {ID: "b", Name: "Shop", Path: remote}, {ID: "c", Name: "Tools", Path: local},
		{ID: "d", Name: "Notes", Path: plain}, {ID: "e", Name: "Gone", Path: filepath.Join(root, "gone")}, {ID: "f", Name: "Trợ lý máy"},
	}
	return ExportOptions{
		HomeDir: home, ProjectRoot: proot, KeyPath: filepath.Join(home, "key"), Projects: refs, Passphrase: pass,
		Snapshot: func(dest string) error { return os.WriteFile(dest, []byte(read(t, dbPath)), 0o600) },
	}, paths
}

func sqlExec(dbPath, q string, args ...any) (sql.Result, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Exec(q, args...)
}

func one(t *testing.T, dbPath, q string, args ...any) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var s sql.NullString
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

// ADR-274: an office goes to another machine whole: its database, files and
// every project's code, what was not pushed or committed included; paths
// follow; logins and temporary copies stay behind.
func TestExportImportWholeOffice(t *testing.T) {
	ctx := context.Background()
	eo, old := oldOffice(t)
	var buf bytes.Buffer
	m, err := Export(ctx, &buf, eo)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, p := range m.Projects {
		kinds[p.Name] = p.Kind
		if p.Name == "Office" && p.Untracked != 0 {
			t.Errorf("the office's data folder went as untracked code (%d files)", p.Untracked)
		}
	}
	want := map[string]string{"Office": KindRemote, "Shop": KindRemote, "Tools": KindGit, "Notes": KindPlain, "Gone": KindMissing, "Trợ lý máy": KindHelper}
	want["Office"] = KindGit // no remote
	for n, k := range want {
		if kinds[n] != k {
			t.Errorf("%s kind = %q, want %q", n, kinds[n], k)
		}
	}
	if bytes.Contains(buf.Bytes(), []byte("LOGIN")) || bytes.Contains(buf.Bytes(), []byte("v3 uncommitted")) {
		t.Fatal("the archive is not encrypted")
	}

	// the new machine: a fresh office checkout elsewhere
	newRoot := t.TempDir()
	nproot := filepath.Join(newRoot, "office-proj")
	nhome := filepath.Join(nproot, ".office")
	io := ImportOptions{HomeDir: nhome, ProjectRoot: nproot, KeyPath: filepath.Join(nhome, "key"), Passphrase: pass, Mode: ModeClone}

	bad := io
	bad.Passphrase = "wrong passphrase here"
	if _, err := Import(ctx, bytes.NewReader(buf.Bytes()), bad); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	rep, err := Import(ctx, bytes.NewReader(buf.Bytes()), io)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rep.Projects {
		if p.Name != "Gone" && !p.OK {
			t.Errorf("%s: %s", p.Name, p.Note)
		}
	}

	// the project with a remote: cloned, the unpushed commit, the uncommitted change, the untracked file; never the ignored one
	shop := filepath.Join(newRoot, "shop")
	if got := read(t, filepath.Join(shop, "app.txt")); got != "v3 uncommitted\n" {
		t.Errorf("shop app.txt = %q", got)
	}
	if log := run(t, shop, "log", "--format=%s"); !strings.Contains(log, "not pushed yet") {
		t.Errorf("unpushed commit lost: %s", log)
	}
	if b := strings.TrimSpace(run(t, shop, "rev-parse", "--abbrev-ref", "HEAD")); b != "main" {
		t.Errorf("branch = %q", b)
	}
	if read(t, filepath.Join(shop, "notes/todo.md")) != "untracked\n" {
		t.Error("untracked file lost")
	}
	if exists(filepath.Join(shop, "secret.env")) {
		t.Error("an ignored file travelled")
	}
	// the local repo: its history, no remote, its change
	tools := filepath.Join(newRoot, "tools")
	if log := run(t, tools, "log", "--format=%s"); !strings.Contains(log, "first") || !strings.Contains(log, "second") {
		t.Errorf("tools history: %s", log)
	}
	if r := strings.TrimSpace(run(t, tools, "remote")); r != "" {
		t.Errorf("tools remote = %q", r)
	}
	if read(t, filepath.Join(tools, "a.txt")) != "three dirty\n" {
		t.Error("tools change lost")
	}
	// the folder without git: its files and link, not node_modules
	notes := filepath.Join(newRoot, "notes folder")
	if read(t, filepath.Join(notes, "sub/x.txt")) != "x\n" || read(t, filepath.Join(notes, "link.md")) != "plain\n" {
		t.Error("plain folder not restored")
	}
	if exists(filepath.Join(notes, "node_modules")) {
		t.Error("node_modules travelled")
	}
	// the office's files: attachments and account settings, not the login, sessions or worktrees
	if read(t, filepath.Join(nhome, "attachments/att_1/pic.png")) != "png" || !exists(filepath.Join(nhome, "accounts/prv_1/settings.json")) {
		t.Error("office files not restored")
	}
	for _, p := range []string{"accounts/prv_1/.credentials.json", "accounts/prv_1/session-env", "worktrees"} {
		if exists(filepath.Join(nhome, p)) {
			t.Errorf("%s travelled", p)
		}
	}
	if read(t, filepath.Join(nhome, "key")) != "KEY" {
		t.Error("key not restored")
	}

	// the database: paths follow, history kept, everyone signed out, clone mode quiet
	db := filepath.Join(nhome, "office.db")
	if p := one(t, db, `SELECT path FROM repos WHERE id = 'b'`); p != shop {
		t.Errorf("repo path = %q, want %q", p, shop)
	}
	if p := one(t, db, `SELECT path FROM repos WHERE id = 'a'`); p != nproot {
		t.Errorf("office project path = %q, want %q", p, nproot)
	}
	perms := one(t, db, `SELECT permissions FROM agents`)
	if !strings.Contains(perms, `"`+tools+`"`) || !strings.Contains(perms, `"`+old["local"]+`x"`) {
		t.Errorf("permissions = %s (a lookalike path must not move)", perms)
	}
	if h := one(t, db, `SELECT cli_home FROM providers`); h != filepath.Join(nhome, "accounts/prv_1") {
		t.Errorf("cli_home = %q", h)
	}
	if msg := one(t, db, `SELECT content FROM messages`); !strings.Contains(msg, old["remote"]) {
		t.Errorf("history rewritten: %q", msg)
	}
	if n := one(t, db, `SELECT count(*) FROM sessions`); n != "0" {
		t.Errorf("sessions = %s", n)
	}
	if one(t, db, `SELECT enabled FROM automations`) != "0" || one(t, db, `SELECT enabled FROM channels`) != "0" {
		t.Error("clone mode left a bot or an automation on")
	}
	if n := one(t, db, `SELECT count(*) FROM settings WHERE key = 'peers'`); n != "0" {
		t.Error("clone mode kept the peer pairings")
	}

	// a second import over it needs --force, and keeps the old database aside
	if _, err := Import(ctx, bytes.NewReader(buf.Bytes()), io); !errors.Is(err, ErrExists) {
		t.Fatalf("import over a database: %v", err)
	}
}

// ModeMove keeps bots, automations and peers as they were.
func TestImportMoveKeepsBots(t *testing.T) {
	ctx := context.Background()
	eo, _ := oldOffice(t)
	eo.Projects = nil // the database and files are enough here
	var buf bytes.Buffer
	if _, err := Export(ctx, &buf, eo); err != nil {
		t.Fatal(err)
	}
	nhome := filepath.Join(t.TempDir(), ".office")
	if _, err := Import(ctx, &buf, ImportOptions{HomeDir: nhome, Passphrase: pass}); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(nhome, "office.db")
	if one(t, db, `SELECT enabled FROM channels`) != "1" || one(t, db, `SELECT count(*) FROM settings WHERE key = 'peers'`) != "1" {
		t.Error("move mode changed bots or peers")
	}
}

func TestExportNeedsLongPassphrase(t *testing.T) {
	if _, err := Export(context.Background(), &bytes.Buffer{}, ExportOptions{Passphrase: "short", Snapshot: func(string) error { return nil }}); err == nil {
		t.Fatal("a short passphrase was taken")
	}
}

func TestSafeJoin(t *testing.T) {
	// whatever the name, the entry lands inside the folder (.. is clamped)
	for name, want := range map[string]string{"a/b": "/d/a/b", "../x": "/d/x", "/etc/passwd": "/d/etc/passwd", "a/../../x": "/d/x", "": "", "..": ""} {
		if got := safeJoin("/d", name); got != want {
			t.Errorf("safeJoin(%q) = %q, want %q", name, got, want)
		}
	}
}

// Defer: a running office keeps its database until ApplyPending at its next start.
func TestImportDeferredUntilRestart(t *testing.T) {
	ctx := context.Background()
	eo, _ := oldOffice(t)
	eo.Projects = nil
	var buf bytes.Buffer
	if _, err := Export(ctx, &buf, eo); err != nil {
		t.Fatal(err)
	}
	nhome := filepath.Join(t.TempDir(), ".office")
	db := filepath.Join(nhome, "office.db")
	if err := os.MkdirAll(nhome, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlExec(db, `CREATE TABLE running (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	rep, err := Import(ctx, &buf, ImportOptions{HomeDir: nhome, KeyPath: filepath.Join(nhome, "k"), Passphrase: pass, Defer: true, Mode: ModeClone})
	if err != nil || !rep.Pending {
		t.Fatalf("deferred import: %+v, %v", rep, err)
	}
	if one(t, db, `SELECT count(*) FROM sqlite_master WHERE name = 'running'`) != "1" {
		t.Fatal("the running office's database was replaced before its restart")
	}
	ok, err := ApplyPending(ctx, nhome, db, filepath.Join(nhome, "k"))
	if err != nil || !ok {
		t.Fatalf("ApplyPending = %v, %v", ok, err)
	}
	if one(t, db, `SELECT count(*) FROM sqlite_master WHERE name = 'repos'`) != "1" || one(t, db, `SELECT enabled FROM channels`) != "0" {
		t.Fatal("the imported database is not in place (or clone mode not applied)")
	}
	if read(t, filepath.Join(nhome, "k")) != "KEY" {
		t.Error("key not in place")
	}
	if ok, _ := ApplyPending(ctx, nhome, db, ""); ok {
		t.Error("applied twice")
	}
}
