package api

import "testing"

func TestSourceBase(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"git@github.com:tung1998/ai-manager-agents.git": "https://github.com/tung1998/ai-manager-agents/blob/HEAD/",
		"https://bitbucket.org/acme/shop.git":           "https://bitbucket.org/acme/shop/src/HEAD/",
		"https://user:tok@github.com/a/b":               "https://github.com/a/b/blob/HEAD/",
		"git@gitlab.com:a/b.git":                        "",
		"":                                              "",
	} {
		if got := sourceBase(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
