package channels

import (
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Command is one of a bot's slash commands: the office's own, or a custom one
// (an automation whose trigger is the command, ADR-049).
type Command struct {
	Name        string // a-z, 0-9, "-" (Telegram shows it with "_")
	Description string
	Arg         string // the text typed after it ("" = none)
	Optional    bool   // the text may be left out
}

// Builtins are the commands every bot has.
func Builtins() []Command {
	return []Command{
		{"create-conversation", "Bật hội thoại: tiếp tục từ câu trả lời gần nhất, không cần tag", "", false},
		{"close-conversation", "Kết thúc hội thoại: mỗi tin được trả lời riêng", "", false},
		{"create-thread", "Tạo thread từ câu trả lời gần nhất, tên ở sau (chuột phải tin → Apps để chọn tin)", "tên thread", true},
		{"pending", "Xem những gì agent đề xuất đang chờ duyệt", "", false},
		{"approve", "Duyệt đề xuất theo số (hoặc all), chỉ Admin", "number", false},
		{"reject", "Từ chối đề xuất theo số (hoặc all), chỉ Admin", "number", false},
		{"skip", "Bỏ qua đề xuất theo số (hoặc all): từ chối, agent không chạy tiếp, chỉ Admin", "number", false},
	}
}

// Reserved are names a custom command may not take.
var Reserved = []string{"job", "create-conversation", "close-conversation", "create-thread", "create-conversion", "close-conversion", "start", "help", "pending", "approve", "approve-always", "reject", "skip", "mode", "cho-duyet", "duyet", "tu-choi", "bo-qua"}

// CommandName makes a name safe for Discord and Telegram menus: lower case,
// no accents, words joined by "-", at most 32 characters ("" = nothing left).
func CommandName(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(s), "/"), "đ", "d"), "Đ", "d")
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r): // an accent
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 32 {
		out = strings.Trim(out[:32], "-")
	}
	return out
}

// commandName reads "/name rest" (any case, "_" for "-", Telegram's
// /name@bot): the name as CommandName makes it, and the text after it.
func commandName(text string) (name, rest string, ok bool) {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "/") || len(s) < 2 {
		return "", "", false
	}
	head, rest, _ := strings.Cut(strings.TrimPrefix(s, "/"), " ")
	head, _, _ = strings.Cut(head, "@")
	head = CommandName(strings.ReplaceAll(head, "_", "-"))
	return head, strings.TrimSpace(rest), head != ""
}

// menu is the commands an adapter was given: a message that is one of them
// is for the bot even without a tag.
type menu struct {
	mu    sync.Mutex
	names map[string]bool
}

func (m *menu) set(cmds []Command) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.names = map[string]bool{}
	for _, c := range cmds {
		m.names[c.Name] = true
	}
}

// has: a command the bot offers (its own always count, before any menu is set).
func (m *menu) has(text string) bool {
	name, _, ok := commandName(text)
	if !ok {
		return false
	}
	if _, _, builtin := command(text); builtin {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.names[name]
}

// argName is a command option's name for Discord (lower case, "-" for spaces).
func argName(s string) string {
	if n := CommandName(s); n != "" {
		return n
	}
	return "noi-dung"
}

// clip cuts s to n characters (Discord's limit for descriptions: 100).
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// MaxCommands is what Discord and Telegram take; more and the menu fails whole.
const MaxCommands = 100

// capped keeps the first MaxCommands (the office's own come first).
func capped(cmds []Command) []Command {
	if len(cmds) > MaxCommands {
		slog.Warn("channels: too many commands for the menu", "have", len(cmds), "kept", MaxCommands)
		return cmds[:MaxCommands]
	}
	return cmds
}
