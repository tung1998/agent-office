package chat

import (
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestPickAgent(t *testing.T) {
	lead := routeProfile{agent: storage.Agent{ID: "lead", Name: "Lead"}, text: "Lead\nTrưởng nhóm\nĐiều phối công việc, chia việc cho mọi người"}
	fe := routeProfile{agent: storage.Agent{ID: "fe", Name: "Frontend"}, text: "Frontend\nLập trình giao diện\nVue, Nuxt, CSS, trang dashboard"}
	be := routeProfile{agent: storage.Agent{ID: "be", Name: "Backend"}, text: "Backend\nLập trình server\nGo, API, database SQLite"}
	qa := routeProfile{agent: storage.Agent{ID: "qa", Name: "QA", Disabled: true}, text: "QA\nKiểm thử\ntest giao diện, test API"}
	all := []routeProfile{lead, fe, be}
	tests := []struct {
		name     string
		text     string
		current  string
		profiles []routeProfile
		want     string // "" = the chat's agent keeps it
	}{
		{"ui words go to frontend", "sửa giao diện trang dashboard bị lệch CSS", "lead", all, "fe"},
		{"accents and case ignored", "GIAO DIEN dashboard loi", "lead", all, "fe"},
		{"api words go to backend", "API trả lỗi 500 khi ghi database", "lead", all, "be"},
		{"nothing matches: chat's agent", "chào buổi sáng", "lead", all, ""},
		{"chat's agent fits as well: stays", "chia việc điều phối giao diện", "lead", all, ""},
		{"already the best: no switch", "sửa CSS dashboard", "fe", all, ""},
		{"tie between two others: stays", "lập trình", "lead", all, ""},
		{"common words only: stays", "giúp mình xem lại cái này nhé", "lead", all, ""},
		{"paused agent never picked", "kiểm thử", "lead", append([]routeProfile{qa}, all...), ""},
		{"one agent only", "giao diện", "fe", []routeProfile{fe}, ""},
		{"empty text", "", "lead", all, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ok := pickAgent(tt.text, tt.current, tt.profiles)
			if got := map[bool]string{true: a.ID}[ok]; got != tt.want {
				t.Fatalf("pickAgent(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestRouteWords(t *testing.T) {
	got := routeWords("Sửa giúp mình giao diện, giao diện ko đẹp!")
	want := []string{"giao", "dien", "dep"}
	if len(got) != len(want) {
		t.Fatalf("routeWords = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("routeWords = %v, want %v", got, want)
		}
	}
}
