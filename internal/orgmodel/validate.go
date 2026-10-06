package orgmodel

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// ValidationError lists every structural problem of an org model.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "mô hình không hợp lệ: " + strings.Join(e.Problems, "; ")
}

// Validate checks a model's structure. It is the single rule set used for
// built-ins, admin edits and imports, so every org model runs on the same core.
func Validate(t Template) error {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }

	if !keyRe.MatchString(t.Key) {
		add("key %q phải là chữ thường, số, dấu gạch ngang", t.Key)
	}
	if strings.TrimSpace(t.Name) == "" {
		add("thiếu tên mô hình")
	}
	switch t.Kind {
	case storage.KindSolo, storage.KindTeam, storage.KindCouncil, storage.KindCustom:
	default:
		add("loại %q không hợp lệ", t.Kind)
	}
	if len(t.Agents) == 0 {
		add("cần ít nhất một agent")
	}

	byKey := map[string]AgentSpec{}
	leads := 0
	for _, a := range t.Agents {
		if !keyRe.MatchString(a.Key) {
			add("agent key %q không hợp lệ", a.Key)
		}
		if _, dup := byKey[a.Key]; dup {
			add("agent key %q bị trùng", a.Key)
		}
		byKey[a.Key] = a
		if strings.TrimSpace(a.Name) == "" {
			add("agent %q thiếu tên", a.Key)
		}
		switch a.Tier {
		case storage.TierLead:
			leads++
		case storage.TierManager, storage.TierWorker:
		default:
			add("agent %q có cấp %q không hợp lệ", a.Key, a.Tier)
		}
		if !storage.ValidTier(a.ModelTier) {
			add("agent %q có hạng model %q không hợp lệ (strong|balanced|fast)", a.Key, a.ModelTier)
		}
	}
	if len(t.Agents) > 0 && leads == 0 {
		add("cần ít nhất một agent cấp lead")
	}
	for _, a := range t.Agents {
		for _, r := range a.ReportsTo {
			target, ok := byKey[r]
			switch {
			case r == a.Key:
				add("agent %q không thể báo cáo cho chính mình", a.Key)
			case !ok:
				add("agent %q báo cáo cho %q không tồn tại", a.Key, r)
			case target.Tier == storage.TierWorker:
				add("agent %q không thể báo cáo cho worker %q", a.Key, r)
			}
		}
		if a.Tier != storage.TierLead && len(a.ReportsTo) == 0 {
			add("agent %q (%s) phải báo cáo cho ít nhất một agent", a.Key, a.Tier)
		}
		if a.Tier == storage.TierLead && len(a.ReportsTo) > 0 {
			add("agent lead %q không báo cáo cho ai", a.Key)
		}
	}
	if cyc := findCycle(t.Agents); cyc != "" {
		add("vòng báo cáo: %s", cyc)
	}

	switch t.Kind {
	case storage.KindSolo:
		if len(t.Agents) != 1 {
			add("mô hình solo chỉ có đúng 1 agent (dùng loại custom nếu cần nhiều hơn)")
		}
	case storage.KindCouncil:
		if leads < 2 {
			add("mô hình hội đồng cần ít nhất 2 lead cùng cấp")
		}
		if t.Governance.Quorum < 1 || t.Governance.Quorum > leads {
			add("quorum %d phải nằm trong 1..%d (số lead)", t.Governance.Quorum, leads)
		}
	}
	for _, v := range t.Governance.Veto {
		if a, ok := byKey[v]; !ok || a.Tier != storage.TierLead {
			add("quyền phủ quyết chỉ gán cho lead có thật, %q không hợp lệ", v)
		}
	}
	if len(p) > 0 {
		return &ValidationError{Problems: p}
	}
	return nil
}

func findCycle(agents []AgentSpec) string {
	edges := map[string][]string{}
	for _, a := range agents {
		edges[a.Key] = a.ReportsTo
	}
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var found string
	var visit func(string) bool
	visit = func(k string) bool {
		state[k] = active
		stack = append(stack, k)
		for _, n := range edges[k] {
			if _, ok := edges[n]; !ok {
				continue
			}
			if state[n] == active {
				found = strings.Join(append(stack, n), " → ")
				return true
			}
			if state[n] == unseen && visit(n) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[k] = done
		return false
	}
	for _, a := range agents {
		if state[a.Key] == unseen && visit(a.Key) {
			return found
		}
	}
	return ""
}
