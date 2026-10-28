package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/templates/packs"
)

// The config registry (ADR-045): every setting an agent (or the office
// assistant, or the CLI) may change, behind four generic tools. A change is
// applied by calling the very handler the dashboard uses, as the person who
// approved it, so validation, storage and the change log have one path.

// cfgKind is one kind of setting.
type cfgKind struct {
	name, title string
	office      bool // not tied to a project (providers, budget)
	input       any  // the handler's input: the fields a patch may set
	merge       bool // the handler replaces the whole input: send the current one with the patch on top
	get         func(s *server, ctx context.Context, id, projectID string) (obj any, project string, err error)
	list        func(s *server, ctx context.Context, projectID string) ([]map[string]any, error)
	create      *cfgRoute
	update      *cfgRoute
	del         *cfgRoute
}

// cfgRoute is the dashboard handler for an op, and the {id} it is called with.
type cfgRoute struct {
	method  string
	handler func(s *server) http.HandlerFunc
	pathID  func(s *server, ctx context.Context, id, projectID string) (string, error)
}

type projectInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

var byID = func(_ *server, _ context.Context, id, _ string) (string, error) { return id, nil }
var byProject = func(_ *server, _ context.Context, _, projectID string) (string, error) { return projectID, nil }

func cfgKinds() []cfgKind {
	return []cfgKind{
		{name: "automation", title: "Tự động hóa", input: automationInput{}, merge: true,
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				a, err := s.cfg.Store.Automations().Get(ctx, id)
				return s.toAutomationDTO(fakeRequest(ctx), a), a.ProjectID, err
			},
			list: func(s *server, ctx context.Context, projectID string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Automations().List(ctx, projectID)
				out := []map[string]any{}
				for _, a := range list {
					out = append(out, map[string]any{"id": a.ID, "name": a.Name, "source": a.Source, "action": a.Action, "enabled": a.Enabled})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createAutomation }, byProject},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateAutomation }, byID},
			del:    &cfgRoute{"DELETE", func(s *server) http.HandlerFunc { return s.deleteAutomation }, byID}},
		{name: "agent", title: "Agent", input: agentInput{}, merge: true,
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				a, err := s.cfg.Store.Agents().Get(ctx, id)
				return toAgentDTO(a), a.ProjectID, err
			},
			list: func(s *server, ctx context.Context, projectID string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Agents().List(ctx, projectID)
				out := []map[string]any{}
				for _, a := range list {
					out = append(out, map[string]any{"id": a.ID, "name": a.Name, "key": a.Key, "role": a.Role, "level": perm.Agent(a), "model_tier": a.ModelTier, "enabled": !a.Disabled})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createAgent }, byProject},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateAgent }, byID},
			del:    &cfgRoute{"DELETE", func(s *server) http.HandlerFunc { return s.deleteAgent }, byID}},
		// read only: the office's agent library, where a new agent starts from
		{name: "agent_library", title: "Thư viện agent", office: true, input: struct{}{},
			get: func(_ *server, _ context.Context, id, _ string) (any, string, error) {
				a, err := team.LibraryAgentByID(id)
				return a, "", err
			},
			list: func(_ *server, _ context.Context, _ string) ([]map[string]any, error) {
				list, err := team.Library()
				out := []map[string]any{}
				for _, a := range list {
					out = append(out, map[string]any{"id": a.ID, "name": a.Name, "category": a.Category, "description": a.Description})
				}
				return out, err
			}},
		// read only: office's library of project packs (ADR-199), for propose_create_project pack=<ref>
		{name: "project_pack", title: "Mẫu project (thư viện)", office: true, input: struct{}{},
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				_, e, err := packs.Find(id)
				if err != nil {
					return e, "", err
				}
				raw, err := packs.Read(e.Ref) // the whole pack: what a project made from it should have (ADR-206)
				return map[string]any{"pack": e, "content": json.RawMessage(raw)}, "", err
			},
			list: func(s *server, ctx context.Context, _ string) ([]map[string]any, error) {
				list, err := s.packList(ctx)
				out := []map[string]any{}
				for _, p := range list {
					out = append(out, map[string]any{"id": p.Ref, "title": p.Title, "description": p.Description, "root": p.Root})
				}
				return out, err
			}},
		{name: "collection", title: "Bảng dữ liệu (collection)", input: collectionInput{}, merge: true,
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				c, err := s.cfg.Store.Collections().ByID(ctx, id)
				return collectionInput{Key: c.Key, Name: c.Name, Description: c.Description, Fields: c.Fields}, c.ProjectID, err
			},
			list: func(s *server, ctx context.Context, projectID string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Collections().List(ctx, projectID)
				out := []map[string]any{}
				for _, c := range list {
					out = append(out, map[string]any{"id": c.ID, "key": c.Key, "name": c.Name, "pack": c.Pack, "fields": len(c.Fields)})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createCollection }, byProject},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateCollection }, byID}},
		{name: "monitor", title: "Giám sát", input: monitorInput{},
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				m, err := s.cfg.Store.Monitors().Get(ctx, id)
				return s.toMonitorDTO(fakeRequest(ctx), m, false), m.ProjectID, err
			},
			list: func(s *server, ctx context.Context, projectID string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Monitors().List(ctx, projectID)
				out := []map[string]any{}
				for _, m := range list {
					out = append(out, map[string]any{"id": m.ID, "name": m.Name, "type": m.Type, "target": m.Target, "enabled": m.Enabled})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createMonitor }, byProject},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateMonitor }, byID},
			del:    &cfgRoute{"DELETE", func(s *server) http.HandlerFunc { return s.deleteMonitor }, byID}},
		{name: "process", title: "Tiến trình", input: processInput{},
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				p, err := s.cfg.Store.Processes().Get(ctx, id)
				return map[string]any{"id": p.ID, "name": p.Name, "command": p.Command, "cwd": p.Cwd, "kind": p.Kind, "source": p.Source,
					"autostart": p.Autostart, "autorestart": p.Autorestart}, p.ProjectID, err
			},
			list: func(s *server, ctx context.Context, projectID string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Processes().List(ctx, projectID)
				out := []map[string]any{}
				for _, p := range list {
					out = append(out, map[string]any{"id": p.ID, "name": p.Name, "command": p.Command, "kind": p.Kind})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createProcess }, byProject},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateProcess }, byID},
			del:    &cfgRoute{"DELETE", func(s *server) http.HandlerFunc { return s.deleteProcess }, byID}},
		{name: "workflow", title: "Quy trình", input: workflowInput{},
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				w, err := s.cfg.Store.Workflows().Get(ctx, id)
				return map[string]any{"id": w.ID, "key": w.Key, "name": w.Name, "source": w.Source, "bindings": w.Bindings, "enabled": w.Enabled}, w.ProjectID, err
			},
			list: func(s *server, ctx context.Context, projectID string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Workflows().List(ctx, projectID)
				out := []map[string]any{}
				for _, w := range list {
					out = append(out, map[string]any{"id": w.ID, "key": w.Key, "name": w.Name, "enabled": w.Enabled, "bindings": w.Bindings})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createWorkflow }, byProject},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateWorkflow }, byID},
			del:    &cfgRoute{"DELETE", func(s *server) http.HandlerFunc { return s.deleteWorkflow }, byID}},
		burnConfigKind(), // ADR-196
		{name: "policy", title: "Quyền & lệnh của project", input: perm.Policy{}, merge: true,
			get: func(s *server, ctx context.Context, _, projectID string) (any, string, error) {
				return perm.LoadPolicy(ctx, s.cfg.Store, projectID), projectID, nil
			},
			update: &cfgRoute{"PUT", func(s *server) http.HandlerFunc { return s.putPolicy }, byProject}},
		{name: "project", title: "Project", input: projectInput{},
			get: func(s *server, ctx context.Context, _, projectID string) (any, string, error) {
				x, err := s.cfg.Store.Repos().Get(ctx, projectID)
				return map[string]any{"name": x.Name, "description": x.Description, "path": x.Path}, projectID, err
			},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateRepo }, byProject}},
		{name: "usage_settings", title: "Ngân sách", office: true, input: usage.Settings{}, merge: true,
			get: func(s *server, ctx context.Context, _, _ string) (any, string, error) {
				if s.cfg.Usage == nil {
					return nil, "", errors.New("office không bật ngân sách")
				}
				st, err := s.cfg.Usage.Settings(ctx)
				return st, "", err
			},
			update: &cfgRoute{"PUT", func(s *server) http.HandlerFunc { return s.usageSettings }, byID}},
		{name: "provider", title: "Kết nối AI", office: true, input: providerInput{}, merge: true,
			get: func(s *server, ctx context.Context, id, _ string) (any, string, error) {
				p, err := s.cfg.Store.Providers().Get(ctx, id)
				return toProviderDTO(p), "", err
			},
			list: func(s *server, ctx context.Context, _ string) ([]map[string]any, error) {
				list, err := s.cfg.Store.Providers().List(ctx)
				out := []map[string]any{}
				for _, p := range list {
					out = append(out, map[string]any{"id": p.ID, "name": p.Name, "kind": string(p.Kind), "is_default": p.IsDefault, "enabled": p.Enabled})
				}
				return out, err
			},
			create: &cfgRoute{"POST", func(s *server) http.HandlerFunc { return s.createProvider }, byID},
			update: &cfgRoute{"PATCH", func(s *server) http.HandlerFunc { return s.updateProvider }, byID},
			del:    &cfgRoute{"DELETE", func(s *server) http.HandlerFunc { return s.deleteProvider }, byID}},
	}
}

func (s *server) cfgKind(name string) (cfgKind, error) {
	for _, k := range cfgKinds() {
		if k.name == name && (k.name != "process" || s.cfg.Ops != nil) && (k.name != "monitor" || s.cfg.Monitors != nil) && (k.name != "workflow" || s.cfg.Workflows != nil) && (k.name != "burn" || s.cfg.Burn != nil) {
			return k, nil
		}
	}
	names := []string{}
	for _, k := range cfgKinds() {
		names = append(names, k.name)
	}
	return cfgKind{}, fmt.Errorf("không có loại cài đặt %q (có: %s)", name, strings.Join(names, ", "))
}

func (k cfgKind) route(op string) *cfgRoute {
	switch op {
	case "create":
		return k.create
	case "update":
		return k.update
	case "delete":
		return k.del
	}
	return nil
}

// fields are the JSON names a patch may set (the handler's input).
func (k cfgKind) fields() []string {
	var out []string
	t := reflect.TypeOf(k.input)
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" && name != "conversation_id" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// state is the setting as a patch sees it: only the input's fields, so a run
// moving last_run_at or a failure count never makes a proposal stale.
func (k cfgKind) state(obj any) map[string]any {
	raw, _ := json.Marshal(obj)
	var all map[string]any
	_ = json.Unmarshal(raw, &all)
	out := map[string]any{}
	for _, f := range k.fields() {
		if v, ok := all[f]; ok {
			out[f] = v
		}
	}
	return out
}

func stateHash(st map[string]any) string {
	raw, _ := json.Marshal(st) // map keys are sorted: the same state, the same hash
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ---- actions.ConfigApplier ----

// CheckChange validates a proposal made in projectID and keeps the setting
// as it is now in c.Before.
func (s *server) CheckChange(ctx context.Context, projectID string, office bool, c *storage.ConfigChange) (string, error) {
	k, err := s.cfgKind(c.Resource)
	if err != nil {
		return "", err
	}
	if k.office && !office {
		return "", fmt.Errorf("%s là cài đặt chung của office: nhờ trợ lý office đề xuất", k.title)
	}
	if k.route(c.Op) == nil {
		return "", fmt.Errorf("%s không hỗ trợ thao tác %q", k.title, c.Op)
	}
	var patch map[string]any
	if len(c.Patch) > 0 && string(c.Patch) != "null" {
		if err := json.Unmarshal(c.Patch, &patch); err != nil {
			return "", errors.New("patch phải là một object JSON")
		}
	}
	fields := k.fields()
	for f := range patch {
		if f == "api_key" || f == "api_key_env" {
			return "", errors.New("không đưa API key qua đề xuất; người dùng tự dán key trên thẻ duyệt")
		}
		if bot, ok := patch["bot"].(map[string]any); ok && f == "bot" && bot["token"] != nil {
			return "", errors.New("không đưa token của bot qua đề xuất; người dùng tự dán token trong form tự động hóa")
		}
		if !slices.Contains(fields, f) {
			return "", fmt.Errorf("trường %q không sửa được (%s có: %s)", f, k.title, strings.Join(fields, ", "))
		}
	}
	if c.Op != "create" {
		obj, project, err := k.get(s, ctx, c.ID, projectID)
		if err != nil {
			return "", fmt.Errorf("không tìm thấy %s %q", k.title, c.ID)
		}
		if !k.office && project != projectID {
			return "", fmt.Errorf("%s này thuộc project khác", k.title)
		}
		st := k.state(obj)
		c.Before, _ = json.Marshal(audit.Snapshot(st))
		c.Hash = stateHash(st)
		return k.title + " " + labelOf(obj, c.ID), nil
	}
	if len(patch) == 0 {
		return "", errors.New("tạo mới cần patch với các trường")
	}
	name, _ := patch["name"].(string)
	return k.title + " " + name, nil
}

// ctxKey for the key a person pasted on a provider's card.
type cfgSecretKey struct{}

// ApplyChange carries out an approved change by calling the dashboard's
// handler as the approver.
func (s *server) ApplyChange(ctx context.Context, a storage.Action) (string, error) {
	c := a.Args.Change
	if c == nil {
		return "", errors.New("thiếu nội dung thay đổi")
	}
	k, err := s.cfgKind(c.Resource)
	if err != nil {
		return "", err
	}
	rt := k.route(c.Op)
	if rt == nil {
		return "", fmt.Errorf("%s không hỗ trợ thao tác %q", k.title, c.Op)
	}
	var patch map[string]any
	_ = json.Unmarshal(c.Patch, &patch)
	body := map[string]any{}
	target := a.ProjectID // the project the change lands in
	if c.Op != "create" {
		obj, project, err := k.get(s, ctx, c.ID, a.ProjectID)
		if err != nil {
			return "", fmt.Errorf("không còn %s %q", k.title, c.ID)
		}
		target = project
		st := k.state(obj)
		now, _ := json.Marshal(audit.Snapshot(st))
		if (c.Hash != "" && stateHash(st) != c.Hash) || (c.Hash == "" && len(c.Before) > 0 && !sameJSON(now, c.Before)) {
			return "", errors.New("đã có thay đổi mới kể từ lúc đề xuất; hãy đề xuất lại")
		}
		if k.merge && c.Op == "update" {
			cur, _ := json.Marshal(obj)
			_ = json.Unmarshal(cur, &body)
		}
	}
	mergeInto(body, patch)
	if key, ok := ctx.Value(cfgSecretKey{}).(string); ok && key != "" && k.name == "provider" {
		body["api_key"] = key
	}
	fields := k.fields()
	for f := range body {
		if !slices.Contains(fields, f) {
			delete(body, f)
		}
	}
	u, err := s.approver(ctx, a.DecidedBy)
	if err != nil {
		return "", err
	}
	if u.Role != storage.RoleAdmin { // ADR-224: its own projects' settings only
		sc, err := s.scopeOf(ctx, u)
		if err != nil {
			return "", err
		}
		if k.office || !sc.has(target) {
			return "", errors.New("chỉ admin của office duyệt được thay đổi này")
		}
	}
	pathID, err := rt.pathID(s, ctx, c.ID, a.ProjectID)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(rt.method, "/", bytes.NewReader(raw)).WithContext(context.WithValue(ctx, ctxUser, u))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", pathID)
	rec := httptest.NewRecorder()
	rt.handler(s)(rec, req)
	if rec.Code >= 400 {
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Error == "" {
			out.Error = http.StatusText(rec.Code)
		}
		return "", errors.New(out.Error)
	}
	return fmt.Sprintf("%s: đã %s", k.title, map[string]string{"create": "tạo", "update": "cập nhật", "delete": "xóa"}[c.Op]), nil
}

// ---- tools: describe, list, get ----

// DescribeConfig lists the kinds of settings, or one kind's fields and ops.
func (s *server) DescribeConfig(kind string) (string, error) {
	if kind == "" {
		var b strings.Builder
		b.WriteString("Các loại cài đặt (describe <loại> để xem trường):\n")
		for _, k := range cfgKinds() {
			if _, err := s.cfgKind(k.name); err != nil {
				continue
			}
			scope := "của project"
			if k.office {
				scope = "của office"
			}
			fmt.Fprintf(&b, "- %s: %s (%s)\n", k.name, k.title, scope)
		}
		return b.String(), nil
	}
	k, err := s.cfgKind(kind)
	if err != nil {
		return "", err
	}
	ops := []string{}
	for _, op := range []string{"create", "update", "delete"} {
		if k.route(op) != nil {
			ops = append(ops, op)
		}
	}
	if k.name == "agent_library" {
		return "Thư viện agent của office (chỉ đọc): list agent_library để tìm, get agent_library <id> để đọc đủ hướng dẫn, quyền, skill. " + fromLibrary, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)\nThao tác: %s\nTrường sửa được:\n", k.title, k.name, strings.Join(ops, ", "))
	t := reflect.TypeOf(k.input)
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" || name == "conversation_id" || name == "api_key" || name == "api_key_env" {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, typeName(f.Type))
	}
	b.WriteString("Đổi bằng propose_change (resource, op, id, patch): patch là object JSON chỉ gồm các trường cần đổi; get để xem giá trị hiện tại.")
	if k.name == "agent" {
		b.WriteString(" " + fromLibrary)
	}
	if k.name == "provider" {
		b.WriteString(" API key không đưa qua patch: người dùng tự dán trên thẻ duyệt.")
	}
	return b.String(), nil
}

// fromLibrary is how a new agent is made: from the library, tailored.
const fromLibrary = "Thêm agent mới: tìm trong agent_library trước; có mẫu hợp thì get nó, rồi propose_change agent create từ mẫu đó (instructions, role, model_tier, permissions, skills) sửa theo project hiện tại (AGENTS.md, skill, lệnh, quy ước của repo) và nói rõ là dựa trên mẫu nào. Chỉ viết từ đầu khi thư viện không có mẫu hợp."

// ListConfig lists the settings of a kind in a project.
func (s *server) ListConfig(ctx context.Context, projectID, kind string) (string, error) {
	k, err := s.cfgKind(kind)
	if err != nil {
		return "", err
	}
	if k.list == nil { // one per project / office
		return s.GetConfig(ctx, projectID, kind, "")
	}
	list, err := k.list(s, ctx, projectID)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(list, "", "  ")
	return string(out), nil
}

// GetConfig is one setting in full (secrets hidden).
func (s *server) GetConfig(ctx context.Context, projectID, kind, id string) (string, error) {
	k, err := s.cfgKind(kind)
	if err != nil {
		return "", err
	}
	obj, project, err := k.get(s, ctx, id, projectID)
	if err != nil {
		return "", fmt.Errorf("không tìm thấy %s %q", k.title, id)
	}
	if !k.office && project != projectID {
		return "", fmt.Errorf("%s này thuộc project khác", k.title)
	}
	out, _ := json.MarshalIndent(audit.Snapshot(obj), "", "  ")
	return string(out), nil
}

// ---- helpers ----

// fakeRequest lets DTO builders that take a request run outside one.
func fakeRequest(ctx context.Context) *http.Request {
	return httptest.NewRequest("GET", "/", nil).WithContext(ctx)
}

func labelOf(obj any, id string) string {
	raw, _ := json.Marshal(obj)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if n, ok := m["name"].(string); ok && n != "" {
		return n
	}
	return id
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// mergeInto applies a JSON merge patch (null removes a key).
func mergeInto(dst, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, pm)
				continue
			}
		}
		dst[k] = v
	}
}

func typeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "chuỗi"
	case reflect.Bool:
		return "true/false"
	case reflect.Int, reflect.Int64, reflect.Float64:
		return "số"
	case reflect.Slice:
		return "danh sách"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return t.String()
}

// approver is the office account a change runs as: the person who approved it,
// or, approved from a bot's chat (by someone the bot lets approve, who has no
// office account), the office's first admin, on the bot's behalf. The log
// still names the chat's approver (ApprovedBy).
func (s *server) approver(ctx context.Context, by string) (storage.User, error) {
	if u, err := s.cfg.Store.Users().GetByEmail(ctx, by); err == nil {
		return u, nil
	}
	if email := actions.PrincipalOf(ctx); email != "" { // an administrator's run, for the admin it acts for (ADR-188)
		if u, err := s.cfg.Store.Users().GetByEmail(ctx, email); err == nil && u.Role == storage.RoleAdmin && !u.Disabled {
			return u, nil
		}
		return storage.User{}, errors.New("người agent đại diện (người bật quyền administrator, hoặc người của lượt chat) không phải admin đang hoạt động")
	}
	if via, _, _ := strings.Cut(by, ":"); via == "discord" || via == "telegram" || via == "bot" {
		users, err := s.cfg.Store.Users().List(ctx)
		if err == nil {
			for _, u := range users {
				if u.Role == storage.RoleAdmin && !u.Disabled {
					return u, nil
				}
			}
		}
	}
	return storage.User{}, errors.New("không rõ người duyệt")
}
