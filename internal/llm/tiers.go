package llm

import (
	"regexp"
	"sort"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// notChat filters out model ids that are not general chat models.
var notChat = regexp.MustCompile(`(?i)(embed|audio|realtime|tts|whisper|transcribe|image|dall-e|vision-preview|moderation|search|davinci|babbage|instruct|computer-use|codex)`)

// SuggestTiers fills empty tiers from the model ids a provider listed, so the
// user never has to know vendor model names. Existing choices are kept.
func SuggestTiers(current map[string]string, models []string) map[string]string {
	out := map[string]string{}
	for k, v := range current {
		out[k] = v
	}
	var chat []string
	for _, m := range models {
		if !notChat.MatchString(m) {
			chat = append(chat, m)
		}
	}
	if len(chat) == 0 {
		return out
	}
	// Newest-looking ids first: higher version numbers sort later lexically.
	sort.Sort(sort.Reverse(sort.StringSlice(chat)))
	small := func(m string) bool {
		l := strings.ToLower(m)
		return strings.Contains(l, "mini") || strings.Contains(l, "nano") || strings.Contains(l, "haiku") || strings.Contains(l, "flash") ||
			strings.Contains(l, "small") || strings.Contains(l, "lite")
	}
	var big, fast []string
	for _, m := range chat {
		if small(m) {
			fast = append(fast, m)
		} else {
			big = append(big, m)
		}
	}
	pick := func(list []string, fallback []string) string {
		if len(list) > 0 {
			return list[0]
		}
		if len(fallback) > 0 {
			return fallback[0]
		}
		return ""
	}
	if out[storage.TierStrong] == "" {
		out[storage.TierStrong] = pick(big, fast)
	}
	if out[storage.TierBalanced] == "" {
		out[storage.TierBalanced] = pick(big, fast)
	}
	if out[storage.TierFast] == "" {
		out[storage.TierFast] = pick(fast, big)
	}
	return out
}
