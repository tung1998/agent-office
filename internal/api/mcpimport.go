package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-093: the agents a server can be given, its call log, and moving the
// MCP servers of Claude Code / Codex / .mcp.json into office.

// originOf reads where a moved server came from.
func originOf(m storage.MCPServer) (automation.Origin, bool) {
	var o automation.Origin
	if m.OriginRef == "" || json.Unmarshal([]byte(m.OriginRef), &o) != nil || o.Type == "" {
		return o, false
	}
	return o, true
}

type mcpAgentDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"` // "" for the office assistant
	AI      string `json:"ai"`      // the kind of its AI connection
}

// mcpAgents lists the agents a server can be given: the office assistant
// and every project's agents.
func (s *server) mcpAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := []mcpAgentDTO{{ID: mcpgateway.AssistantID, Name: "assistant"}}
	repos, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	kinds := map[string]string{}
	aiOf := func(id string) string {
		if id == "" {
			return ""
		}
		if k, ok := kinds[id]; ok {
			return k
		}
		k := ""
		if p, err := s.cfg.Store.Providers().Get(ctx, id); err == nil {
			k = string(p.Kind)
		}
		kinds[id] = k
		return k
	}
	office := assistant.ID(ctx, s.cfg.Store)
	for _, p := range repos {
		if p.ID == office {
			continue
		}
		model, err := s.cfg.Store.OrgModels().GetForRepo(ctx, p.ID)
		if err != nil {
			continue
		}
		agents, err := s.cfg.Store.Agents().List(ctx, model.ID)
		if err != nil {
			continue
		}
		for _, a := range agents {
			out = append(out, mcpAgentDTO{ID: a.ID, Name: a.Name, Project: p.Name, AI: aiOf(a.ProviderID)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

type mcpCallDTO struct {
	ID             string    `json:"id"`
	Server         string    `json:"server"`
	Tool           string    `json:"tool"`
	Caller         string    `json:"caller"`
	CallerKind     string    `json:"caller_kind"`
	ProjectID      string    `json:"project_id"`
	ConversationID string    `json:"conversation_id"`
	ActionID       string    `json:"action_id"`
	Status         string    `json:"status"`
	Error          string    `json:"error"`
	DurationMS     int64     `json:"duration_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

// mcpCalls is the call log of one server (or of all), newest first.
func (s *server) mcpCalls(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.cfg.Store.MCPCalls().List(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]mcpCallDTO, 0, len(list))
	for _, c := range list {
		out = append(out, mcpCallDTO{ID: c.ID, Server: c.ServerName, Tool: c.Tool, Caller: c.Caller, CallerKind: c.CallerKind,
			ProjectID: c.ProjectID, ConversationID: c.ConversationID, ActionID: c.ActionID, Status: c.Status, Error: c.Error,
			DurationMS: c.DurationMS, CreatedAt: c.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": out, "keep_days": mcpgateway.CallLogDays})
}

// ---- moving servers into office ----

type mcpCandidate struct {
	Ref       automation.Ref `json:"ref"`
	Name      string         `json:"name"`      // in its source
	Suggested string         `json:"suggested"` // a valid office name
	Source    string         `json:"source"`    // its location label
	Transport string         `json:"transport"`
	Target    string         `json:"target"`  // url or command
	Movable   bool           `json:"movable"` // office can take it out of the source
	Taken     bool           `json:"taken"`   // office already has a server of that name
	MovedAs   string         `json:"moved_as,omitempty"`
	Problem   string         `json:"problem,omitempty"` // why office cannot move it
	Disabled  bool           `json:"disabled,omitempty"`
}

var badNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

// suggestName turns a source name into an office one.
func suggestName(n string) string {
	n = strings.Trim(badNameChars.ReplaceAllString(strings.ToLower(n), "-"), "-")
	if len(n) > 40 {
		n = strings.Trim(n[:40], "-")
	}
	if n == "office" {
		n = "office-mcp"
	}
	return n
}

// mcpImportList lists the MCP servers of this machine office can move in.
func (s *server) mcpImportList(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Automation == nil {
		writeError(w, http.StatusNotFound, "office không quản lý MCP của máy")
		return
	}
	ctx := r.Context()
	have, err := s.cfg.Store.MCPServers().List(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	names := map[string]bool{}
	moved := map[string]string{} // type|path|project|name → office name
	for _, m := range have {
		names[m.Name] = true
		if o, ok := originOf(m); ok {
			moved[o.Type+"|"+o.Path+"|"+o.ProjectPath+"|"+o.Name] = m.Name
		}
	}
	out := []mcpCandidate{}
	for _, it := range s.cfg.Automation.Scan(ctx).Items {
		if it.Kind != "mcp" {
			continue
		}
		loc := it.Location
		c := mcpCandidate{Ref: automation.Ref{Kind: "mcp", Name: it.Name, Type: loc.Type, Path: loc.Path, ProjectPath: loc.ProjectPath},
			Name: it.Name, Suggested: suggestName(it.Name), Source: loc.Label, Disabled: it.Disabled}
		c.Transport, _ = it.Meta["transport"].(string)
		c.Target, _ = it.Meta["url"].(string)
		if c.Target == "" {
			c.Target, _ = it.Meta["command"].(string)
		}
		_, c.Movable = automation.MovableTypes[loc.Type]
		c.Taken = names[c.Suggested]
		c.MovedAs = moved[loc.Type+"|"+loc.Path+"|"+loc.ProjectPath+"|"+it.Name]
		if c.Transport == "sse" {
			c.Problem = "sse"
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
}

// expandVars fills ${VAR} from office's environment (Claude Code does the
// same); missing ones are reported and left as they are.
func expandVars(v string, missing map[string]bool) string {
	return varRe.ReplaceAllStringFunc(v, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		def := ""
		if n, d, ok := strings.Cut(name, ":-"); ok {
			name, def = n, d
		}
		if val, ok := os.LookupEnv(name); ok {
			return val
		}
		if def != "" {
			return def
		}
		missing[name] = true
		return m
	})
}

var varRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func stringMap(v any, missing map[string]bool) map[string]string {
	out := map[string]string{}
	if m, ok := v.(map[string]any); ok {
		for k, x := range m {
			s, _ := x.(string)
			out[k] = expandVars(s, missing)
		}
	}
	return out
}

// serverFromConfig builds an office server from a Claude-style config.
func (s *server) serverFromConfig(name string, cfg map[string]any, missing map[string]bool) (storage.MCPServer, error) {
	m := storage.MCPServer{Name: name, Scope: "machine", Enabled: true}
	box := s.cfg.Providers.Box()
	kind, _ := cfg["type"].(string)
	url, _ := cfg["url"].(string)
	command, _ := cfg["command"].(string)
	switch {
	case kind == "sse":
		return m, errors.New("MCP dạng SSE cũ: office chưa chuyển tiếp được, hãy dùng địa chỉ streamable HTTP của server")
	case url != "":
		m.Kind, m.URL = "http", expandVars(url, missing)
		enc, err := mcpgateway.SealMap(box, stringMap(cfg["headers"], missing))
		if err != nil {
			return m, err
		}
		m.HeadersEnc = enc
	case command != "":
		m.Kind, m.Command = "stdio", expandVars(command, missing)
		m.Args = []string{}
		if args, ok := cfg["args"].([]any); ok {
			for _, a := range args {
				if v, _ := a.(string); v != "" {
					m.Args = append(m.Args, expandVars(v, missing))
				}
			}
		}
		enc, err := mcpgateway.SealMap(box, stringMap(cfg["env"], missing))
		if err != nil {
			return m, err
		}
		m.EnvEnc = enc
	default:
		return m, errors.New("cấu hình không có URL hay lệnh chạy")
	}
	return m, nil
}

type mcpImportResult struct {
	Name     string        `json:"name"`
	Source   string        `json:"source"`
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	TakenOut bool          `json:"taken_out"`
	Missing  []string      `json:"missing,omitempty"` // ${VAR}s office's environment lacks
	Server   *mcpServerDTO `json:"server,omitempty"`
}

// mcpImport moves servers into office: each is created from its source's
// config (secrets into the encrypted store) and, with take_out, removed
// from the source (a copy of the file kept in trash) so the AI does not
// load it twice.
func (s *server) mcpImport(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Automation == nil {
		writeError(w, http.StatusNotFound, "office không quản lý MCP của máy")
		return
	}
	var in struct {
		Items []struct {
			Ref  automation.Ref `json:"ref"`
			Name string         `json:"name"`
		} `json:"items"`
		TakeOut bool `json:"take_out"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Items) == 0 || len(in.Items) > 50 {
		writeError(w, http.StatusBadRequest, "chọn từ 1 đến 50 MCP")
		return
	}
	ctx := r.Context()
	out := make([]mcpImportResult, 0, len(in.Items))
	for _, x := range in.Items {
		res := mcpImportResult{Name: firstNonEmptyStr(strings.TrimSpace(x.Name), suggestName(x.Ref.Name))}
		fail := func(err error) {
			res.Error = err.Error()
			out = append(out, res)
		}
		it, cfg, err := s.cfg.Automation.MCPConfig(ctx, x.Ref)
		if err != nil {
			fail(err)
			continue
		}
		res.Source = it.Location.Label
		if !mcpgateway.ValidName(res.Name) {
			fail(errors.New(`tên chỉ gồm chữ thường, số và "-", không quá 40 ký tự, không dùng "office"`))
			continue
		}
		missing := map[string]bool{}
		m, err := s.serverFromConfig(res.Name, cfg, missing)
		if err != nil {
			fail(err)
			continue
		}
		for k := range missing {
			res.Missing = append(res.Missing, k)
		}
		origin := automation.Origin{Type: it.Location.Type, Path: it.Location.Path, ProjectPath: it.Location.ProjectPath,
			Name: it.Name, Label: it.Location.Label, MovedAt: time.Now().UTC().Format(time.RFC3339)}
		if rawCfg, err := json.Marshal(cfg); err == nil {
			origin.ConfigEnc, _ = s.cfg.Providers.Box().Seal(string(rawCfg))
		}
		m.Origin = firstNonEmptyStr(automation.MovableTypes[it.Location.Type], it.Location.Type)
		raw, _ := json.Marshal(origin)
		m.OriginRef = string(raw)
		m, err = s.cfg.Store.MCPServers().Create(ctx, m)
		if errors.Is(err, storage.ErrConflict) {
			fail(errors.New("office đã có MCP tên " + res.Name + ": đặt tên khác"))
			continue
		}
		if err != nil {
			fail(err)
			continue
		}
		_, movable := automation.MovableTypes[it.Location.Type]
		if in.TakeOut && movable {
			backup, terr := s.cfg.Automation.TakeOutMCP(ctx, it)
			if terr != nil {
				res.Error = "đã chép vào office nhưng chưa gỡ được khỏi " + it.Location.Label + ": " + terr.Error()
			} else {
				res.TakenOut = true
				origin.Backup = backup
				raw, _ = json.Marshal(origin)
				m.OriginRef = string(raw)
				_ = s.cfg.Store.MCPServers().Update(ctx, m)
			}
		}
		res.OK = true
		s.audit(r, audit.Change{Action: "mcp_server.import", ResourceID: m.ID, After: s.auditMCP(m),
			Detail: map[string]any{"from": it.Location.Label, "source_name": it.Name, "taken_out": res.TakenOut}})
		d := s.mcpServerDTO(m)
		res.Server = &d
		s.checkMCPLater(m)
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// mcpPutBack writes a moved server back where it came from (its config as
// office has it now) and, with delete, removes it from office.
func (s *server) mcpPutBack(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Automation == nil {
		writeError(w, http.StatusNotFound, "office không quản lý MCP của máy")
		return
	}
	var in struct {
		Delete bool `json:"delete"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	m, err := s.cfg.Store.MCPServers().Get(ctx, r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	o, ok := originOf(m)
	if !ok {
		writeError(w, http.StatusBadRequest, "MCP này tạo trong office, không có chỗ để trả về")
		return
	}
	box := s.cfg.Providers.Box()
	cfg := map[string]any{}
	// the config as the source had it; else rebuilt from office's
	if plain, err := box.Open(o.ConfigEnc); o.ConfigEnc != "" && err == nil && json.Unmarshal([]byte(plain), &cfg) == nil && len(cfg) > 0 {
		// as it was
	} else if m.Kind == "stdio" {
		env, err := mcpgateway.OpenMap(box, m.EnvEnc)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		args := make([]any, 0, len(m.Args))
		for _, a := range m.Args {
			args = append(args, a)
		}
		cfg["command"], cfg["args"] = m.Command, args
		if len(env) > 0 {
			cfg["env"] = anyMap(env)
		}
	} else {
		headers, err := mcpgateway.OpenMap(box, m.HeadersEnc)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		cfg["type"], cfg["url"] = "http", m.URL
		if len(headers) > 0 {
			cfg["headers"] = anyMap(headers)
		}
	}
	if err := s.cfg.Automation.PutBackMCP(ctx, o, cfg); err != nil {
		s.autoError(w, r, err)
		return
	}
	if in.Delete {
		if err := s.cfg.Store.MCPServers().Delete(ctx, m.ID); err != nil {
			s.internal(w, r, err)
			return
		}
		s.cfg.Gateway.Forget(m.ID)
	} else {
		m.OriginRef, m.Origin = "", "manual" // its copy is back in the source
		if err := s.cfg.Store.MCPServers().Update(ctx, m); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	s.audit(r, audit.Change{Action: "mcp_server.put_back", ResourceID: m.ID, Before: s.auditMCP(m),
		Detail: map[string]any{"to": o.Label, "name": o.Name, "deleted": in.Delete}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "to": o.Label})
}

func anyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
