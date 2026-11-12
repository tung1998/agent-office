package chat_test

import (
	"testing"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-277: with AutoRoute on, the person's untagged message goes to the
// agent whose role fits it; off (the default), the chat's agent answers.
func TestAutoRouteUntaggedMessage(t *testing.T) {
	g := newGroup(t)
	ui, err := g.f.st.Agents().Create(g.context, storage.Agent{ProjectID: g.f.project.ID, Key: "ui", Name: "Thiết kế",
		Role: "Thiết kế giao diện", Description: "Vue, CSS, bố cục trang dashboard", ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	g.context = chat.WithAutoRoute(g.context)
	const msg = "bố cục giao diện CSS trang dashboard bị vỡ"
	if got := g.sendAll(t, msg); len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("off: authors = %v, want the chat's agent %s", got, g.leadNm)
	}
	pol := perm.LoadPolicy(g.context, g.f.st, g.f.project.ID)
	pol.AutoRoute = true
	if err := perm.SavePolicy(g.context, g.f.st, g.f.project.ID, pol); err != nil {
		t.Fatal(err)
	}
	if got := g.sendAll(t, msg); len(got) != 1 || got[0] != ui.Name {
		t.Fatalf("on: authors = %v, want %s", got, ui.Name)
	}
	if got := g.sendAll(t, "cảm ơn"); len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("on, nothing fits: authors = %v, want %s", got, g.leadNm)
	}
	conv, _ := g.f.st.Chat().GetConversation(g.context, g.conv.ID)
	if conv.AgentID != g.lead {
		t.Fatalf("the chat's agent changed to %s", conv.AgentName)
	}
	// not marked as the person's own message (relay, automation…): the chat's agent
	g2 := newGroup(t)
	if _, err := g2.f.st.Agents().Create(g2.context, storage.Agent{ProjectID: g2.f.project.ID, Key: "ui", Name: "Thiết kế",
		Role: "Thiết kế giao diện", Description: "Vue, CSS, bố cục trang dashboard", ModelTier: "fast"}); err != nil {
		t.Fatal(err)
	}
	pol2 := perm.LoadPolicy(g2.context, g2.f.st, g2.f.project.ID)
	pol2.AutoRoute = true
	_ = perm.SavePolicy(g2.context, g2.f.st, g2.f.project.ID, pol2)
	if got := g2.sendAll(t, msg); len(got) != 1 || got[0] != g2.leadNm {
		t.Fatalf("unmarked: authors = %v, want %s", got, g2.leadNm)
	}
}
