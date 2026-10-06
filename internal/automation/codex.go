package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Codex keeps its MCP servers in ~/.codex/config.toml as [mcp_servers.<name>]
// tables. Office reads the subset they use (strings, string arrays, inline
// tables of strings, and the .env / .http_headers sub-tables) to move a
// server into office (ADR-093), and removes the tables with a backup.

// codexTable splits a table header "[mcp_servers.a.env]" into its keys.
func codexTable(line string) ([]string, bool) {
	if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") {
		return nil, false
	}
	inner, ok := strings.CutSuffix(strings.TrimSpace(strings.SplitN(line, "#", 2)[0]), "]")
	if !ok {
		return nil, false
	}
	inner = strings.TrimPrefix(inner, "[")
	var keys []string
	for inner != "" {
		inner = strings.TrimLeft(inner, " \t.")
		if inner == "" {
			break
		}
		if inner[0] == '"' || inner[0] == '\'' {
			end := strings.IndexByte(inner[1:], inner[0])
			if end < 0 {
				return nil, false
			}
			keys = append(keys, inner[1:end+1])
			inner = inner[end+2:]
			continue
		}
		k, rest, _ := strings.Cut(inner, ".")
		keys = append(keys, strings.TrimSpace(k))
		inner = rest
	}
	return keys, true
}

// stripComment drops a "# …" comment that is not inside a string.
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote == 0 && c == '#':
			return line[:i]
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == '"' && c == '\\':
			i++
		case c == quote:
			quote = 0
		}
	}
	return line
}

// codexString reads a TOML string at the start of s.
func codexString(s string) (string, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", s, false
	}
	switch s[0] {
	case '"':
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				v, err := strconv.Unquote(s[:i+1])
				return v, s[i+1:], err == nil
			}
		}
	case '\'':
		if end := strings.IndexByte(s[1:], '\''); end >= 0 {
			return s[1 : end+1], s[end+2:], true
		}
	}
	return "", s, false
}

// codexValue reads a string, an array of strings or an inline table of
// strings at the start of s (other values: ok false).
func codexValue(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	switch s[0] {
	case '"', '\'':
		v, _, ok := codexString(s)
		return v, ok
	case '[':
		var out []any
		rest := s[1:]
		for {
			rest = strings.TrimLeft(rest, " \t\r\n,")
			if rest == "" || rest[0] == ']' {
				return out, true
			}
			v, r, ok := codexString(rest)
			if !ok {
				return nil, false
			}
			out, rest = append(out, v), r
		}
	case '{':
		out := map[string]any{}
		rest := s[1:]
		for {
			rest = strings.TrimLeft(rest, " \t,")
			if rest == "" || rest[0] == '}' {
				return out, true
			}
			var key string
			if rest[0] == '"' || rest[0] == '\'' {
				k, r, ok := codexString(rest)
				if !ok {
					return nil, false
				}
				key, rest = k, r
			} else {
				i := strings.IndexByte(rest, '=')
				if i < 0 {
					return nil, false
				}
				key, rest = strings.TrimSpace(rest[:i]), rest[i:]
			}
			rest = strings.TrimLeft(rest, " \t")
			if !strings.HasPrefix(rest, "=") {
				return nil, false
			}
			v, r, ok := codexString(rest[1:])
			if !ok {
				return nil, false
			}
			out[key], rest = v, r
		}
	}
	return nil, false
}

// codexServer reads server name's config from Codex's TOML in the shape of
// Claude's (command/args/env or url/headers).
func codexServer(p, name string) (map[string]any, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	section := "" // "" outside, "." the server, else its sub-table
	lines := strings.Split(string(raw), "\n")
	found := false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripComment(lines[i]))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if keys, ok := codexTable(line); ok {
			section = ""
			if len(keys) >= 2 && keys[0] == "mcp_servers" && keys[1] == name {
				found = true
				section = "."
				if len(keys) == 3 {
					section = keys[2]
				} else if len(keys) > 3 {
					section = "-" // tools.<x> and the like
				}
			}
			continue
		}
		if section == "" || section == "-" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.Trim(strings.TrimSpace(k), `"'`)
		v = strings.TrimSpace(v)
		// a multi-line array: read on to its closing bracket
		if strings.HasPrefix(v, "[") && !strings.Contains(v, "]") {
			for i+1 < len(lines) && !strings.Contains(v, "]") {
				i++
				v += " " + strings.TrimSpace(stripComment(lines[i]))
			}
		}
		val, ok := codexValue(v)
		if !ok {
			continue
		}
		if section != "." {
			m, _ := out[section].(map[string]any)
			if m == nil {
				m = map[string]any{}
				out[section] = m
			}
			m[k] = val
			continue
		}
		out[k] = val
	}
	if !found {
		return nil, ErrNotFound
	}
	cfg := map[string]any{}
	for _, k := range []string{"command", "args", "env", "url"} {
		if v, ok := out[k]; ok {
			cfg[k] = v
		}
	}
	headers := map[string]any{}
	if h, ok := out["http_headers"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = v
		}
	}
	if envVar, _ := out["bearer_token_env_var"].(string); envVar != "" {
		if tok := os.Getenv(envVar); tok != "" {
			headers["Authorization"] = "Bearer " + tok
		} else {
			headers["Authorization"] = "Bearer ${" + envVar + "}"
		}
	}
	if len(headers) > 0 {
		cfg["headers"] = headers
	}
	if cfg["url"] != nil {
		cfg["type"] = "http"
	}
	return cfg, nil
}

// removeCodexServer deletes server name's tables from Codex's TOML, keeping
// the file as it was in trash.
func (in Installer) removeCodexServer(p, name string) (string, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	var kept []string
	skip, found := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		if keys, ok := codexTable(strings.TrimSpace(line)); ok {
			skip = len(keys) >= 2 && keys[0] == "mcp_servers" && keys[1] == name
			found = found || skip
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	if !found {
		return "", errors.New("không thấy MCP này trong cấu hình Codex")
	}
	if err := os.MkdirAll(in.Trash, 0o700); err != nil {
		return "", err
	}
	backup := filepath.Join(in.Trash, time.Now().Format("20060102-150405")+"-codex-config.toml")
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return "", err
	}
	return backup, os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o600)
}
