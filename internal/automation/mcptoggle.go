package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Turning a machine MCP server off and on: Claude Code stops loading it, its
// config is kept, and turning it on gives the source file back as it was.
//
//   - local (~/.claude.json projects[<path>].mcpServers): Claude Code's own
//     per-project list projects[<path>].disabledMcpServers (what the /mcp
//     toggle writes).
//   - project (<path>/.mcp.json, untouched): disabledMcpjsonServers in
//     <path>/.claude/settings.local.json (untracked; a disabledMcpjsonServers
//     entry rejects the server in any settings file, trusted folder or not).
//   - user (~/.claude.json mcpServers): Claude Code has no machine-wide off
//     switch (disabledMcpServers is per project), so office keeps the
//     server's config as the file had it (encrypted, in its settings) and
//     takes it out of the file; turning it on puts it back at its place.
//
// Plugins, Codex, Cursor and Claude Desktop have no switch.

// ToggleTypes are the sources whose MCP servers office turns off and on,
// with how.
var ToggleTypes = map[string]string{"user": "office", "local": "disabledMcpServers", "project": "disabledMcpjsonServers"}

// DisabledMCP is office's record of a server it turned off.
type DisabledMCP struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	ProjectPath string          `json:"project_path,omitempty"`
	Index       int             `json:"index,omitempty"`  // user: its place in mcpServers
	Config      json.RawMessage `json:"config,omitempty"` // user: its config as the file had it
	AddedKey    bool            `json:"added_key,omitempty"`
	AddedFile   bool            `json:"added_file,omitempty"`
	AddedDir    bool            `json:"added_dir,omitempty"`
	At          string          `json:"at"`
}

// Key identifies the server the record is for.
func (d DisabledMCP) Key() string { return d.Type + "|" + d.ProjectPath + "|" + d.Name }

// MCPStash keeps the records (the user config holds secrets: the store
// encrypts them).
type MCPStash interface {
	Load(ctx context.Context) (map[string]DisabledMCP, error)
	Save(ctx context.Context, recs map[string]DisabledMCP) error
}

var ErrNoStash = errors.New("office chưa cất được cấu hình MCP để tắt")

func settingsLocal(projectPath string) string {
	return filepath.Join(projectPath, ".claude", "settings.local.json")
}

// SetMCPEnabled turns an installed MCP server off or on.
func (s *Service) SetMCPEnabled(ctx context.Context, r Ref, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.resolve(ctx, r)
	if err != nil {
		return err
	}
	return s.setEnabled(ctx, it, on)
}

func (s *Service) setEnabled(ctx context.Context, it Item, on bool) error {
	loc := it.Location
	if it.Kind != "mcp" || !loc.Editable || ToggleTypes[loc.Type] == "" {
		return ErrNotAllowed
	}
	if it.Disabled != on { // already so
		return nil
	}
	if s.Stash == nil {
		return ErrNoStash
	}
	recs, err := s.Stash.Load(ctx)
	if err != nil {
		return err
	}
	key := DisabledMCP{Type: loc.Type, Name: it.Name, ProjectPath: loc.ProjectPath}.Key()
	rec, had := recs[key]
	if !on {
		rec = DisabledMCP{Type: loc.Type, Name: it.Name, ProjectPath: loc.ProjectPath, At: time.Now().UTC().Format(time.RFC3339)}
	}
	in := s.Installer
	switch loc.Type {
	case "user":
		if on {
			if !had {
				return ErrNotFound
			}
			_, err = in.editJSON(loc.Path, false, func(doc *jsonObj) error {
				servers, err := doc.child("mcpServers")
				if err != nil {
					return err
				}
				if servers.has(it.Name) {
					return ErrExists
				}
				servers.insert(rec.Index, it.Name, rec.Config)
				return doc.setChild("mcpServers", servers)
			})
			if err != nil {
				return err
			}
			delete(recs, key)
			return s.Stash.Save(ctx, recs)
		}
		_, err = in.editJSON(loc.Path, false, func(doc *jsonObj) error {
			servers, err := doc.child("mcpServers")
			if err != nil {
				return err
			}
			raw, idx, ok := servers.remove(it.Name)
			if !ok {
				return ErrNotFound
			}
			rec.Config, rec.Index = raw, idx
			return doc.setChild("mcpServers", servers)
		})
		if err != nil {
			return err
		}
		// only stashed once the file is actually written, so the two never disagree
		recs[key] = rec
		return s.Stash.Save(ctx, recs)
	case "local":
		_, err = in.editJSON(loc.Path, false, func(doc *jsonObj) error {
			projects, err := doc.child("projects")
			if err != nil {
				return err
			}
			pc, err := projects.child(loc.ProjectPath)
			if err != nil {
				return err
			}
			if err := toggleList(pc, "disabledMcpServers", it.Name, on, &rec); err != nil {
				return err
			}
			if err := projects.setChild(loc.ProjectPath, pc); err != nil {
				return err
			}
			return doc.setChild("projects", projects)
		})
	case "project":
		p := settingsLocal(loc.ProjectPath)
		if !on {
			_, e1 := os.Stat(filepath.Dir(p))
			_, e2 := os.Stat(p)
			rec.AddedDir, rec.AddedFile = os.IsNotExist(e1), os.IsNotExist(e2)
		}
		var doc *jsonObj
		doc, err = in.editJSON(p, !on, func(doc *jsonObj) error {
			return toggleList(doc, "disabledMcpjsonServers", it.Name, on, &rec)
		})
		if err == nil && on && rec.AddedFile && len(doc.keys) == 0 {
			_ = os.Remove(p)
			if rec.AddedDir {
				_ = os.Remove(filepath.Dir(p)) // only when empty
			}
		}
	}
	if err != nil {
		return err
	}
	if on {
		delete(recs, key)
	} else {
		recs[key] = rec
	}
	return s.Stash.Save(ctx, recs)
}

// toggleList adds name to (off) or drops it from (on) the string list key;
// a list office added goes away with its last name.
func toggleList(o *jsonObj, key, name string, on bool, rec *DisabledMCP) error {
	var list []string
	if raw, ok := o.vals[key]; ok {
		if err := json.Unmarshal(raw, &list); err != nil {
			return fmt.Errorf("%s không phải danh sách tên", key)
		}
	} else if !on {
		rec.AddedKey = true
	}
	if on {
		list = slices.DeleteFunc(list, func(n string) bool { return n == name })
		if len(list) == 0 && rec.AddedKey {
			o.remove(key)
			return nil
		}
	} else if !slices.Contains(list, name) {
		list = append(list, name)
	}
	if list == nil {
		list = []string{}
	}
	return o.set(key, list)
}

// disabledNames reads a string list of a JSON object.
func disabledNames(m map[string]any, key string) map[string]bool {
	out := map[string]bool{}
	if arr, ok := m[key].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

// forgetDisabled turns a server back on before it leaves its source (so no
// list keeps its name); a user one office keeps is just dropped.
func (s *Service) forgetDisabled(ctx context.Context, it Item) (handled bool, err error) {
	if it.Kind != "mcp" || !it.Disabled {
		return false, nil
	}
	if it.Location.Type != "user" {
		return false, s.setEnabled(ctx, it, true)
	}
	if s.Stash == nil {
		return true, ErrNoStash
	}
	recs, err := s.Stash.Load(ctx)
	if err != nil {
		return true, err
	}
	delete(recs, DisabledMCP{Type: "user", Name: it.Name}.Key())
	return true, s.Stash.Save(ctx, recs)
}

// stashedUser lists the user servers office keeps while they are off.
func stashedUser(recs map[string]DisabledMCP, path string, have map[string]any) []Item {
	servers := map[string]any{}
	for _, r := range recs {
		if r.Type != "user" || have[r.Name] != nil {
			continue
		}
		var c map[string]any
		if json.Unmarshal(r.Config, &c) == nil && c != nil {
			servers[r.Name] = c
		}
	}
	items := mcpItems(servers, Location{Type: "user", Label: "Toàn máy", Path: path, Editable: true})
	for i := range items {
		items[i].Disabled = true
	}
	return items
}

// ---- JSON files edited in place: key order and untouched values kept ----

// jsonObj is a JSON object that keeps its keys' order and its values' bytes.
type jsonObj struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObj(raw []byte) (*jsonObj, error) {
	o := &jsonObj{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil {
		return nil, err
	} else if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errors.New("không phải object JSON")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := o.vals[k]; !dup {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func (o *jsonObj) bytes() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshalPlain(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o *jsonObj) has(k string) bool { _, ok := o.vals[k]; return ok }

// child is the object under k (empty when missing).
func (o *jsonObj) child(k string) (*jsonObj, error) {
	c, err := parseObj(o.vals[k])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k, err)
	}
	return c, nil
}

func (o *jsonObj) setRaw(k string, v json.RawMessage) {
	if !o.has(k) {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsonObj) set(k string, v any) error {
	raw, err := marshalPlain(v)
	if err != nil {
		return err
	}
	o.setRaw(k, raw)
	return nil
}

func (o *jsonObj) setChild(k string, c *jsonObj) error {
	raw, err := c.bytes()
	if err != nil {
		return err
	}
	o.setRaw(k, raw)
	return nil
}

// remove drops k, returning its value and place.
func (o *jsonObj) remove(k string) (json.RawMessage, int, bool) {
	i := slices.Index(o.keys, k)
	if i < 0 {
		return nil, 0, false
	}
	v := o.vals[k]
	o.keys = slices.Delete(o.keys, i, i+1)
	delete(o.vals, k)
	return v, i, true
}

// insert puts k at place i (the end when past it).
func (o *jsonObj) insert(i int, k string, v json.RawMessage) {
	if o.has(k) {
		o.vals[k] = v
		return
	}
	i = min(max(i, 0), len(o.keys))
	o.keys = slices.Insert(o.keys, i, k)
	o.vals[k] = v
}

// editJSON read-modify-writes a JSON object file in Claude Code's layout
// (2-space indent), keeping a copy of it in trash; the write is atomic and
// follows a symlinked file. With create, a missing file starts empty.
func (in Installer) editJSON(p string, create bool, fn func(*jsonObj) error) (*jsonObj, error) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	raw, err := os.ReadFile(p)
	exists := err == nil
	if err != nil && !(create && os.IsNotExist(err)) {
		return nil, err
	}
	doc, err := parseObj(raw)
	if err != nil {
		return nil, fmt.Errorf("%s không phải JSON hợp lệ: %w", filepath.Base(p), err)
	}
	if err := fn(doc); err != nil {
		return nil, err
	}
	compact, err := doc.bytes()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	if !exists || bytes.HasSuffix(raw, []byte("\n")) {
		out.WriteByte('\n')
	}
	mode := os.FileMode(0o644)
	if exists {
		if _, err := in.backup(p, filepath.Base(p)); err != nil {
			return nil, err
		}
		if st, err := os.Stat(p); err == nil {
			mode = st.Mode().Perm()
		}
	} else if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".office-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return nil, err
	}
	return doc, os.Rename(tmp.Name(), p)
}
