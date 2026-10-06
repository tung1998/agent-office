// Package audit records who changed what (ADR-043): a person, an agent (and
// the person who approved it) or an automation, where it came from (chat,
// job, task, proposal), through which channel, and the value before/after.
// Every write to audit_log goes through Record.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/storage"
)

// Who did it, and where from.
type Who struct {
	Kind       string // human | agent | automation | system
	ID         string
	Name       string
	ApprovedBy string // the person who approved an agent's proposal
	Via        string // ui | chat | task | assistant | mcp | automation | api

	ConversationID, JobID, TaskID, ActionID string
}

type key struct{}

func With(ctx context.Context, w Who) context.Context { return context.WithValue(ctx, key{}, w) }

func From(ctx context.Context) (Who, bool) {
	w, ok := ctx.Value(key{}).(Who)
	return w, ok
}

// fromActor reads the older actor string ("human:<email>", "auto:<name>", …).
func fromActor(ctx context.Context) Who {
	a := actor.From(ctx)
	switch {
	case strings.HasPrefix(a, "human:"):
		return Who{Kind: "human", Name: strings.TrimPrefix(a, "human:"), Via: "ui"}
	case strings.HasPrefix(a, "user:"): // a person known by id (e.g. logout)
		id := strings.TrimPrefix(a, "user:")
		return Who{Kind: "human", ID: id, Name: id, Via: "ui"}
	case strings.HasPrefix(a, "auto:"):
		return Who{Kind: "automation", Name: strings.TrimPrefix(a, "auto:"), Via: "automation"}
	}
	return Who{Kind: "system", Name: a}
}

// Change is one change to record.
type Change struct {
	Action     string // resource.verb, e.g. automation.update
	Resource   string // "" = the part of Action before the dot
	ResourceID string
	ProjectID  string
	Before     any
	After      any
	Detail     map[string]any
	Err        error
}

// Entry builds the audit row for c, done by the Who in ctx.
func Entry(ctx context.Context, c Change) storage.AuditEntry {
	w, ok := From(ctx)
	if !ok {
		w = fromActor(ctx)
	}
	res := c.Resource
	if res == "" {
		res, _, _ = strings.Cut(c.Action, ".")
	}
	detail := c.Detail
	if c.Err != nil {
		detail = make(map[string]any, len(c.Detail)+1)
		for k, v := range c.Detail {
			detail[k] = v
		}
		detail["error"] = c.Err.Error()
	}
	e := storage.AuditEntry{
		Actor: w.Kind + ":" + w.Name, Action: c.Action, Target: c.ResourceID, Detail: detail,
		ActorKind: w.Kind, ActorID: w.ID, ActorName: w.Name, ApprovedBy: w.ApprovedBy, Via: w.Via,
		ProjectID: c.ProjectID, ConversationID: w.ConversationID, JobID: w.JobID, TaskID: w.TaskID, ActionID: w.ActionID,
		Resource: res, ResourceID: c.ResourceID, OK: c.Err == nil,
	}
	e.Before, e.After = snapshotPair(c.Before, c.After)
	if e.Detail != nil {
		d := make(map[string]any, len(e.Detail))
		for k, v := range e.Detail {
			d[k] = v
		}
		redact(d)
		e.Detail = d
	}
	return e
}

// Record appends c; a failure is logged, never silently dropped.
func Record(ctx context.Context, repo storage.AuditRepo, c Change) error {
	err := repo.Append(ctx, Entry(ctx, c))
	if err != nil {
		slog.Error("audit: ghi nhật ký thất bại", "action", c.Action, "resource", c.ResourceID, "err", err)
	}
	return err
}

// raw is v as a JSON object, secrets included (nil = none).
func raw(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok && m == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{"value": string(b)}
	}
	return m
}

// snapshotPair redacts before and after together, so a secret that changed
// still shows it changed ("*** (cũ)" → "*** (mới)") without its value.
func snapshotPair(before, after any) (map[string]any, map[string]any) {
	b, a := raw(before), raw(after)
	markChanged(b, a)
	redact(b)
	redact(a)
	return b, a
}

func markChanged(b, a map[string]any) {
	if b == nil || a == nil {
		return
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			continue
		}
		if bm, ok := bv.(map[string]any); ok {
			if am, ok := av.(map[string]any); ok {
				markChanged(bm, am)
			}
			continue
		}
		if isSecret(k) && secretSet(av) && secretSet(bv) && fmt.Sprint(av) != fmt.Sprint(bv) {
			b[k], a[k] = "*** (cũ)", "*** (mới)" // i18n-ignore
		}
	}
}

func secretSet(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && !strings.HasPrefix(s, "***")
}

// Snapshot turns v into a JSON object with secrets replaced by "***"
// (an empty secret or a yes/no flag stays as is, so the log still shows
// "not set" / "has a key").
func Snapshot(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok && m == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{"value": string(b)}
	}
	redact(m)
	return m
}

func redact(m map[string]any) {
	for k, v := range m {
		if isSecret(k) {
			switch x := v.(type) {
			case nil, bool: // "has_api_key": says whether, not what
			case string:
				if x != "" && !strings.HasPrefix(x, "***") {
					m[k] = "***"
				}
			default:
				m[k] = "***"
			}
			continue
		}
		redactValue(v)
	}
}

func redactValue(v any) {
	switch x := v.(type) {
	case map[string]any:
		redact(x)
	case []any:
		for _, e := range x {
			redactValue(e)
		}
	}
}

// isSecret matches names like api_key, APIKey, password, token, x_token,
// SecretHash, authorization; not counts like input_tokens.
func isSecret(name string) bool {
	n := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
	switch n {
	case "password", "token", "secret", "apikey", "authorization", "cookie", "credentials", "dsn", "heartbeatpath":
		return true
	}
	for _, suf := range []string{"token", "secret", "hash", "apikey", "password", "privatekey", "accesskey", "credentials", "dsn"} {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}
