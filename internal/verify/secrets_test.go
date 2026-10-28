package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	// built at run time: no real-looking key sits in the repo
	aws := "AKIA" + strings.Repeat("Q", 16)
	gh := "ghp_" + strings.Repeat("a1", 18)
	pk := "-----BEGIN " + "OPENSSH PRIVATE KEY-----"
	db := "postgres://app:" + "s3cretpass" + "@db:5432/x"
	write := func(name, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	write("a/config.go", "package a\n\nconst k = \""+aws+"\"\n")
	write("deploy.sh", "x\n"+pk+"\n")
	write("README.md", "token "+gh+" gitleaks:allow\nurl postgres://user:${PASS}@db/x\n")
	write("app.yml", "db: "+db+"\nexample: postgres://user:password@localhost/x\n")
	got := Secrets(dir, []string{"a/config.go", "deploy.sh", "README.md", "app.yml", "gone.txt"})
	for _, want := range []string{"`a/config.go:3`: AWS", "`deploy.sh:2`: khóa riêng", "`app.yml:1`: chuỗi kết nối"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "README") || strings.Contains(got, aws) {
		t.Errorf("allowed line flagged, or the value shown:\n%s", got)
	}
	if s := Secrets(dir, []string{"README.md"}); s != "" {
		t.Fatalf("clean file flagged: %s", s)
	}
}
