package trigger

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Vars fill a prompt template.
type Vars struct {
	Payload    any // decoded JSON (nil when not JSON)
	RawPayload string
	Message    string // chat text (Telegram/Discord), or a script's @@agent lines
	Output     string // escalation: what the script printed
	ExitCode   string // escalation: its exit code
	User       string
	Source     string
	Automation string
	Now        time.Time
	Loc        *time.Location
}

var placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`)

// Render fills {{…}}: a fixed set of names, no logic, since payloads are
// untrusted. Unknown names stay as written; missing payload paths are empty.
func Render(tpl string, v Vars) string {
	loc := v.Loc
	if loc == nil {
		loc = time.UTC
	}
	now := v.Now.In(loc)
	return placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		switch name {
		case "payload":
			return v.RawPayload
		case "message":
			return v.Message
		case "output":
			return v.Output
		case "exit_code":
			return v.ExitCode
		case "user":
			return v.User
		case "source":
			return v.Source
		case "automation":
			return v.Automation
		case "now":
			return now.Format("2006-01-02 15:04 MST")
		case "today":
			return now.Format(time.DateOnly)
		case "yesterday":
			return now.AddDate(0, 0, -1).Format(time.DateOnly)
		}
		if path, ok := strings.CutPrefix(name, "payload."); ok {
			return lookup(v.Payload, strings.Split(path, "."))
		}
		return m
	})
}

func lookup(v any, path []string) string {
	for _, p := range path {
		switch x := v.(type) {
		case map[string]any:
			v = x[p]
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(x) {
				return ""
			}
			v = x[i]
		default:
			return ""
		}
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64, bool:
		return fmt.Sprint(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
