package deps

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// ChangesOf is Changes for a diff: a package.json or pyproject.toml counts
// only when the diff changes its dependencies (not its scripts, version or a
// tool's settings), read against the file in root (merged or not yet) or,
// when it cannot be, from the diff's lines alone. A lockfile or requirements
// file always counts; a manifest the diff does not show, or that cannot be
// told apart, counts too (as Changes).
func ChangesOf(root, diff string, files []string) []Change {
	secs := parseDiff(diff)
	kept := make([]string, 0, len(files))
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(f), "./")))
		name := path.Base(clean)
		if name == "package.json" || name == "pyproject.toml" {
			if s := secs[clean]; s != nil && !manifestChanged(root, clean, s) {
				continue
			}
		}
		kept = append(kept, f)
	}
	return Changes(root, kept)
}

// section is one file's part of a unified diff.
type section struct {
	created, deleted bool
	hunks            []hunk
}

// hunk is one @@ block: where it starts on each side and its lines, with
// their ' ', '+' or '-' mark.
type hunk struct {
	oldStart, oldCount, newStart, newCount int
	lines                                  []string
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseDiff splits a unified (git) diff by file, keyed by the file's path
// (its new one; the old one when it was deleted).
func parseDiff(diff string) map[string]*section {
	out := map[string]*section{}
	var cur *section
	var h *hunk
	oldLeft, newLeft := 0, 0
	lines := strings.Split(diff, "\n")
	for i, l := range lines {
		if h != nil && (oldLeft > 0 || newLeft > 0) {
			if l == "" { // a blank context line some tools trim
				l = " "
			}
			switch l[0] {
			case ' ':
				oldLeft--
				newLeft--
			case '-':
				oldLeft--
			case '+':
				newLeft--
			case '\\':
			default:
				h, oldLeft, newLeft = nil, 0, 0
			}
			if h != nil {
				h.lines = append(h.lines, l)
				continue
			}
		}
		switch {
		case strings.HasPrefix(l, "diff --git "):
			cur, h = &section{}, nil
			if f := strings.Fields(l); len(f) == 4 {
				out[diffPath(f[3])] = cur
			}
		case strings.HasPrefix(l, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			if cur == nil || len(cur.hunks) > 0 { // a plain unified diff: no "diff --git" line
				cur = &section{}
			}
			h = nil
			oldP, newP := strings.TrimSpace(l[4:]), strings.TrimSpace(lines[i+1][4:])
			oldP, _, _ = strings.Cut(oldP, "\t")
			newP, _, _ = strings.Cut(newP, "\t")
			cur.created, cur.deleted = cur.created || oldP == "/dev/null", newP == "/dev/null"
			p := newP
			if cur.deleted {
				p = oldP
			}
			for k, v := range out { // keyed by the "diff --git" line already
				if v == cur && k != diffPath(p) {
					delete(out, k)
				}
			}
			out[diffPath(p)] = cur
		case strings.HasPrefix(l, "new file mode") && cur != nil:
			cur.created = true
		case strings.HasPrefix(l, "deleted file mode") && cur != nil:
			cur.deleted = true
		case strings.HasPrefix(l, "@@") && cur != nil:
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			n := func(s string) int {
				if s == "" {
					return 1
				}
				v, _ := strconv.Atoi(s)
				return v
			}
			cur.hunks = append(cur.hunks, hunk{oldStart: n(m[1]), oldCount: n(m[2]), newStart: n(m[3]), newCount: n(m[4])})
			h = &cur.hunks[len(cur.hunks)-1]
			oldLeft, newLeft = h.oldCount, h.newCount
		}
	}
	return out
}

// diffPath is a diff header's path without its a/ or b/ and quotes.
func diffPath(p string) string {
	p = strings.Trim(p, `"`)
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		p = p[2:]
	}
	return path.Clean(p)
}

// manifestChanged: the diff s of the manifest f (package.json,
// pyproject.toml) may change its dependencies. Unknown counts as changed.
func manifestChanged(root, f string, s *section) bool {
	if s.created || s.deleted || len(s.hunks) == 0 {
		return true
	}
	if root != "" {
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			base := strings.Split(string(b), "\n")
			old, nw, ok := rebuild(base, s.hunks)
			if ok {
				if path.Base(f) == "package.json" {
					return jsonDepsDiffer(old, nw)
				}
				return pyDeps(old) != pyDeps(nw)
			}
		}
	}
	for _, h := range s.hunks { // from the diff alone
		if path.Base(f) == "package.json" && jsonHunkDeps(h) || path.Base(f) == "pyproject.toml" && pyHunkDeps(h) {
			return true
		}
	}
	return false
}

// rebuild gives the file before and after the diff from base, which is
// either (tried in that order): the file after it (a merged diff), or
// before it (one not merged yet). ok false: base is neither.
func rebuild(base []string, hs []hunk) (old, nw string, ok bool) {
	if out, ok := apply(base, hs, '+'); ok {
		return out, strings.Join(base, "\n"), true
	}
	if out, ok := apply(base, hs, '-'); ok {
		return strings.Join(base, "\n"), out, true
	}
	return "", "", false
}

// apply turns base, the side from ('+' after, '-' before) of the diff, into
// its other side; ok false when a line does not match.
func apply(base []string, hs []hunk, from byte) (string, bool) {
	var out []string
	at := 0
	for _, h := range hs {
		start, count := h.newStart, h.newCount
		if from == '-' {
			start, count = h.oldStart, h.oldCount
		}
		idx := start - 1
		if count == 0 {
			idx = start
		}
		if idx < at || idx > len(base) {
			return "", false
		}
		out = append(out, base[at:idx]...)
		for _, l := range h.lines {
			c, text := l[0], l[1:]
			switch {
			case c == '\\':
			case c == ' ' || c == from:
				if idx >= len(base) || base[idx] != text {
					return "", false
				}
				if c == ' ' {
					out = append(out, text)
				}
				idx++
			default:
				out = append(out, text)
			}
		}
		at = idx
	}
	out = append(out, base[at:]...)
	return strings.Join(out, "\n"), true
}

// jsonDeps are the package.json keys an install depends on.
var jsonDeps = map[string]bool{
	"dependencies": true, "devDependencies": true, "optionalDependencies": true, "peerDependencies": true,
	"peerDependenciesMeta": true, "bundleDependencies": true, "bundledDependencies": true,
	"overrides": true, "resolutions": true, "pnpm": true, "workspaces": true, "packageManager": true,
}

// jsonDepsDiffer: old and new package.json differ in what an install
// depends on (either one unreadable: they do).
func jsonDepsDiffer(old, nw string) bool {
	var o, n map[string]any
	if json.Unmarshal([]byte(old), &o) != nil || json.Unmarshal([]byte(nw), &n) != nil {
		return true
	}
	for k := range jsonDeps {
		if !reflect.DeepEqual(o[k], n[k]) {
			return true
		}
	}
	return false
}

var jsonKeyRe = regexp.MustCompile(`^\s*"([^"]+)"\s*:`)

// indentOf counts a line's leading spaces (a tab as two).
func indentOf(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 2
		default:
			return n
		}
	}
	return n
}

// topLevel: a package.json key at this indent is the file's own (the usual
// two spaces or a tab; deeper files are told only by their opening brace).
const topLevel = 2

// jsonHunkDeps: a changed line of h may be in one of jsonDeps, read from
// the hunk alone (its indents): unknown counts.
func jsonHunkDeps(h hunk) bool {
	for i, l := range h.lines {
		if l[0] != '+' && l[0] != '-' {
			continue
		}
		key, ok := jsonTopKey(h.lines[:i+1])
		if !ok || jsonDeps[key] {
			return true
		}
	}
	return false
}

// jsonTopKey is the top-level key the last of lines is in, walking up the
// hunk through lines of its side.
func jsonTopKey(lines []string) (string, bool) {
	last := lines[len(lines)-1]
	side, text := last[0], last[1:]
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	cur, key := indentOf(text), ""
	if m := jsonKeyRe.FindStringSubmatch(text); m != nil {
		key = m[1]
		if cur > 0 && cur <= topLevel {
			return key, true
		}
	} else if strings.TrimSpace(strings.TrimRight(text, ",")) == "}" || strings.TrimSpace(strings.TrimRight(text, ",")) == "]" {
		key = "" // a block's closing line: what it closes is above, at the same indent
		cur++
	}
	for j := len(lines) - 2; j >= 0; j-- {
		c, t := lines[j][0], lines[j][1:]
		if c != ' ' && c != side || strings.TrimSpace(t) == "" {
			continue
		}
		ind := indentOf(t)
		if ind >= cur {
			continue
		}
		if strings.TrimSpace(t) == "{" && ind == 0 {
			return key, key != ""
		}
		m := jsonKeyRe.FindStringSubmatch(t)
		if open := strings.TrimSpace(t); m == nil || !strings.HasSuffix(open, "{") && !strings.HasSuffix(open, "[") {
			continue // a value of its own: not a block the line is in
		}
		key, cur = m[1], ind
		if ind > 0 && ind <= topLevel {
			return key, true
		}
	}
	return "", false
}

// pyTables are the pyproject.toml tables an install depends on (and their
// sub-tables); pyKeys the keys of [project] it does.
var (
	pyTables = []string{"project.optional-dependencies", "dependency-groups", "build-system", "tool.uv"}
	pyKeys   = map[string]bool{"dependencies": true, "optional-dependencies": true, "requires-python": true}
)

func pyTable(t string) bool {
	for _, p := range pyTables {
		if t == p || strings.HasPrefix(t, p+".") {
			return true
		}
	}
	return false
}

// tableOf is a [table] or [[table]] line's name ("" = not one).
func tableOf(t string) string {
	t = strings.TrimSpace(t)
	if !strings.HasPrefix(t, "[") || strings.HasPrefix(t, "[\"") {
		return ""
	}
	if i := strings.Index(t, "#"); i > 0 {
		t = strings.TrimSpace(t[:i])
	}
	return strings.TrimSpace(strings.Trim(t, "[]"))
}

// pyDeps is what of a pyproject.toml an install depends on, line by line
// (comments and blanks out): its tables in pyTables, and the keys of
// [project] in pyKeys with all the lines of their value.
func pyDeps(text string) string {
	var b strings.Builder
	table, depth := "", 0
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if depth > 0 { // a [project] key's value on more lines
			b.WriteString(t + "\n")
			depth += strings.Count(t, "[") - strings.Count(t, "]")
			continue
		}
		if name := tableOf(t); name != "" {
			table = name
			if pyTable(table) {
				b.WriteString("[" + table + "]\n")
			}
			continue
		}
		switch {
		case pyTable(table):
			b.WriteString(t + "\n")
		case table == "project":
			k, v, ok := strings.Cut(t, "=")
			if ok && pyKeys[strings.TrimSpace(k)] {
				b.WriteString(t + "\n")
				depth = strings.Count(v, "[") - strings.Count(v, "]")
			}
		}
	}
	return b.String()
}

// pyHunkDeps: a changed line of h may change what an install depends on,
// read from the hunk alone: in a table of pyTables, or in [project] under
// a key of pyKeys. Unknown (no table or key above it in the hunk) counts.
func pyHunkDeps(h hunk) bool {
	for i, l := range h.lines {
		if l[0] != '+' && l[0] != '-' {
			continue
		}
		if strings.TrimSpace(l[1:]) == "" || strings.HasPrefix(strings.TrimSpace(l[1:]), "#") {
			continue
		}
		table, key, found := "", "", false
		for j := i; j >= 0 && !found; j-- {
			c, t := h.lines[j][0], h.lines[j][1:]
			if c != ' ' && c != l[0] {
				continue
			}
			if name := tableOf(t); name != "" {
				table, found = name, true
				continue
			}
			if key == "" && t != "" && t[0] != ' ' && t[0] != '\t' {
				if k, _, ok := strings.Cut(t, "="); ok {
					key = strings.TrimSpace(k)
				}
			}
		}
		if !found || pyTable(table) || table == "project" && (key == "" || pyKeys[key]) {
			return true
		}
	}
	return false
}
