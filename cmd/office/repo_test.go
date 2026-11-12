package main

import "testing"

func TestSameRemote(t *testing.T) {
	ok := [][2]string{
		{"git@github.com:tung1998/ai-manager-agents.git", "https://github.com/tung1998/ai-manager-agents"},
		{"ssh://git@github.com/a/b.git", "git@github.com:a/b"},
		{"", "git@github.com:a/b.git"},
		{"git@github.com:a/b.git", ""},
	}
	for _, c := range ok {
		if err := sameRemote(c[0], c[1]); err != nil {
			t.Errorf("sameRemote(%q, %q) = %v, want nil", c[0], c[1], err)
		}
	}
	if err := sameRemote("git@github.com:a/b.git", "git@github.com:a/c.git"); err == nil {
		t.Error("different repos must not match")
	}
}
