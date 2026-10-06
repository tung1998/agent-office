package api

import (
	"os/exec"
	"testing"
)

func TestCommitSubject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if got := commitSubject("../..", "HEAD"); got == "" {
		t.Fatal("HEAD of this repo has no subject")
	}
	if got := commitSubject("../..", "0000000000000000000000000000000000000000"); got != "" {
		t.Fatalf("unknown revision = %q", got)
	}
	if got := commitSubject(t.TempDir(), "HEAD"); got != "" {
		t.Fatalf("no repo = %q", got)
	}
	if b := loadBuild("dev", ""); b.Subject != "" || b.Version != "dev" {
		t.Fatalf("no source = %+v", b)
	}
}
