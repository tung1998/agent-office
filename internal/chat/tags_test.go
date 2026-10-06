package chat_test

import (
	"context"
	"slices"
	"testing"

	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// An automation's tags go on its chat beside the chat's own: no repeat
// ("bug" = "Bug"), at most 10 in all.
func TestAddTags(t *testing.T) {
	ctx := context.Background()
	f := setup(t, func(*provider.Service) storage.Provider { return storage.Provider{} })
	c, err := f.engine.StartConversationPurpose(ctx, f.project.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.st.Chat().SetConversationTags(ctx, c.ID, []string{"Bug"})
	if err := f.engine.AddTags(ctx, c.ID, []string{"bug", "nightly"}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.st.Chat().GetConversation(ctx, c.ID)
	if slices.Sort(got.Tags); !slices.Equal(got.Tags, []string{"Bug", "nightly"}) {
		t.Fatalf("tags = %v", got.Tags)
	}
	many := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	if err := f.engine.AddTags(ctx, c.ID, many); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.st.Chat().GetConversation(ctx, c.ID); len(got.Tags) != 10 {
		t.Fatalf("tags = %v", got.Tags)
	}
}
