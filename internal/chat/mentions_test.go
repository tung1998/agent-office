package chat

import (
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestMentions(t *testing.T) {
	lead := storage.Agent{ID: "a1", Key: "team-lead", Name: "Trưởng nhóm"}
	dev := storage.Agent{ID: "a2", Key: "engineer", Name: "Dev"}
	devLead := storage.Agent{ID: "a3", Key: "dev-lead", Name: "Dev Lead"}
	agents := []storage.Agent{lead, dev, devLead}
	ids := func(list []storage.Agent) (out []string) {
		for _, a := range list {
			out = append(out, a.ID)
		}
		return
	}
	cases := []struct {
		text string
		want []string
	}{
		{"@Dev sửa giúp", []string{"a2"}},
		{"@trưởng nhóm và @DEV xem", []string{"a1", "a2"}},
		{"nhờ @Dev Lead review", []string{"a3"}},
		{"@team-lead ơi", []string{"a1"}},
		{"`@Dev` và\n```\n@Dev\n```\nkhông ai", nil},
		{"@Dev rồi @Dev lần nữa", []string{"a2"}},
		{"email a@Dev.com", nil},
		{"@Nobody", nil},
		{"~~~\n@Dev\n~~~\nxong", nil},
	}
	for _, c := range cases {
		got := ids(Mentions(c.text, agents))
		if len(got) != len(c.want) {
			t.Errorf("%q → %v, want %v", c.text, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q → %v, want %v", c.text, got, c.want)
			}
		}
	}
}
