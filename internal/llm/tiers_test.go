package llm_test

import (
	"testing"

	"github.com/tung1998/agent-office/internal/llm"
)

func TestSuggestTiers(t *testing.T) {
	got := llm.SuggestTiers(map[string]string{"strong": "keep-me"}, []string{
		"text-embedding-3-large", "gpt-4o-audio-preview", "gpt-4.1", "gpt-4.1-mini", "gpt-5", "gpt-5-mini", "whisper-1",
	})
	if got["strong"] != "keep-me" || got["balanced"] != "gpt-5" || got["fast"] != "gpt-5-mini" {
		t.Fatalf("tiers = %v", got)
	}
	if got := llm.SuggestTiers(nil, []string{"llama3.1:8b"}); got["fast"] != "llama3.1:8b" || got["strong"] != "llama3.1:8b" {
		t.Fatalf("single model = %v", got)
	}
	if got := llm.SuggestTiers(nil, nil); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
}
