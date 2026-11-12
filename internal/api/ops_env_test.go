package api

import (
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// env replaces the whole set; a name a shell would not take is refused.
func TestProcessInputEnv(t *testing.T) {
	t.Parallel()
	p := storage.Process{Name: "dev", Command: "pnpm dev", Kind: "service", Env: map[string]string{"OLD": "1"}}
	env := map[string]string{" PORT ": "3001", "VITE_HOST": "0.0.0.0"}
	if err := (processInput{Env: &env}).apply(&p); err != nil {
		t.Fatal(err)
	}
	if len(p.Env) != 2 || p.Env["PORT"] != "3001" || p.Env["OLD"] != "" {
		t.Fatalf("env: %v", p.Env)
	}
	if err := (processInput{}).apply(&p); err != nil || p.Env["PORT"] != "3001" {
		t.Fatalf("no env in the input keeps it: %v %v", err, p.Env)
	}
	for _, k := range []string{"1PORT", "MY-PORT", "A B", ""} {
		bad := map[string]string{k: "x"}
		if err := (processInput{Env: &bad}).apply(&p); err == nil {
			t.Errorf("%q accepted", k)
		}
	}
}
