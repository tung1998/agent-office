package actor

import "testing"

func TestSource(t *testing.T) {
	for in, want := range map[string]string{"human:a@x.io": "web", "discord:binh": "discord", "telegram:an": "telegram", "auto:Báo cáo": "auto", "": "web", "system": "web"} {
		if got := Source(in); got != want {
			t.Errorf("Source(%q) = %q, want %q", in, got, want)
		}
	}
}
