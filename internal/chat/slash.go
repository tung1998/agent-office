package chat

import (
	"strconv"
	"strings"
	"unicode"
)

// Office's own chat commands (ADR-273), typed in a chat or to a bot:
//
//	/btw <question>        a side question while the agent works: answered
//	                       apart, read only, not part of the chat's history
//	/goal <goal> [--check "cmd"] [--rounds N]
//	                       the chat goes on, turn after turn, until the goal
//	                       is reached (ADR-132's loop for a chat)
//	/goal status | clear   how it goes; call it off (stop, off: as clear)
const (
	SlashBtw  = "btw"
	SlashGoal = "goal"
)

// Slash is one of office's chat commands as typed.
type Slash struct {
	Name   string // btw | goal
	Arg    string // the question, the goal, or status/clear for /goal
	Check  string // /goal --check: a shell command, exit 0 = reached
	Rounds int    // /goal --rounds (0 = the default)
}

// GoalOp is what a /goal asks for: "set" (a goal in Arg), "status" or "clear".
func (s Slash) GoalOp() string {
	switch strings.ToLower(strings.TrimSpace(s.Arg)) {
	case "status", "?":
		return "status"
	case "clear", "stop", "off", "cancel":
		return "clear"
	case "":
		if s.Check == "" {
			return "status"
		}
	}
	return "set"
}

// ParseSlash reads "/btw …" or "/goal …" (any case; Telegram's /goal@bot
// too); ok is false for anything else, a skill or a workflow among them.
func ParseSlash(text string) (Slash, bool) {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "/") {
		return Slash{}, false
	}
	head, rest, _ := strings.Cut(s[1:], " ")
	if i := strings.IndexFunc(rest, func(r rune) bool { return r != ' ' }); i > 0 {
		rest = rest[i:]
	}
	if h, r, ok := strings.Cut(head, "\n"); ok { // "/btw\nquestion"
		head, rest = h, strings.TrimSpace(r+" "+rest)
	}
	head, _, _ = strings.Cut(head, "@")
	switch name := strings.ToLower(head); name {
	case SlashBtw:
		return Slash{Name: SlashBtw, Arg: strings.TrimSpace(rest)}, true
	case SlashGoal:
		out := Slash{Name: SlashGoal}
		out.Arg, out.Check, out.Rounds = goalFlags(rest)
		return out, true
	}
	return Slash{}, false
}

// goalFlags takes --check and --rounds out of a /goal's text (each at the
// start of a word). --check takes a quoted command ("…" or '…'), else the
// rest of the line up to a --rounds.
func goalFlags(s string) (text, check string, rounds int) {
	var b strings.Builder
	atWord := true
	for s != "" {
		if atWord {
			if v, ok := cutFlag(s, "--check"); ok {
				check, s = flagValue(v)
				b.WriteByte(' ')
				continue
			}
			if v, ok := cutFlag(s, "--rounds"); ok {
				num, rest, _ := strings.Cut(v, " ")
				if n, err := strconv.Atoi(strings.TrimSpace(num)); err == nil {
					rounds, s = n, rest
					b.WriteByte(' ')
					continue
				}
			}
		}
		r := []rune(s)[0]
		b.WriteRune(r)
		s = s[len(string(r)):]
		atWord = unicode.IsSpace(r)
	}
	return strings.Join(strings.Fields(b.String()), " "), strings.TrimSpace(check), rounds
}

// cutFlag: s starts with the flag followed by a space or "=" (the rest, its value).
func cutFlag(s, flag string) (string, bool) {
	if !strings.HasPrefix(s, flag) || len(s) == len(flag) || s[len(flag)] != ' ' && s[len(flag)] != '=' {
		return "", false
	}
	return strings.TrimLeft(s[len(flag)+1:], " "), true
}

// flagValue is a flag's value: quoted, or up to the end of the line or a
// following --rounds.
func flagValue(s string) (v, rest string) {
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			return s[1 : end+1], s[end+2:]
		}
		return s[1:], ""
	}
	v, rest, _ = strings.Cut(s, "\n")
	if i := strings.Index(v, " --rounds"); i >= 0 {
		v, rest = v[:i], v[i+1:]+"\n"+rest
	}
	return v, rest
}
