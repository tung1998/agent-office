package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// briefed: when each agent was last told its job changed; changes within
// briefGap of that fold into it (an agent updating itself does not loop).
var briefed sync.Map

const briefGap = 10 * time.Minute

// briefRole tells an agent its job changed (ADR-194): its role, instructions,
// permissions, or it now leads the project. It brings its notes and
// schedules up to date in a chat of its own.
func (s *server) briefRole(r *http.Request, old, a storage.Agent, also ...string) {
	if s.cfg.Chat == nil || a.Disabled {
		return
	}
	var changes []string
	if old.Role != a.Role {
		changes = append(changes, fmt.Sprintf("role: %q → %q", old.Role, a.Role))
	}
	if old.Instructions != a.Instructions && old.ID != "" {
		changes = append(changes, "your instructions were rewritten (they are in your system prompt)")
	}
	if lo, ln := perm.Agent(old), perm.Agent(a); old.ID != "" && lo != ln {
		changes = append(changes, "permission level: "+lo+" → "+ln)
	}
	if oc, nc := capsOf(old), capsOf(a); old.ID != "" && !slices.Equal(oc, nc) {
		var d []string
		for _, c := range nc {
			if !slices.Contains(oc, c) {
				d = append(d, "+"+c)
			}
		}
		for _, c := range oc {
			if !slices.Contains(nc, c) {
				d = append(d, "-"+c)
			}
		}
		if len(d) > 0 {
			changes = append(changes, "capabilities: "+strings.Join(d, " "))
		}
	}
	if old.ID != "" && old.Permissions.FullAccess != a.Permissions.FullAccess {
		changes = append(changes, map[bool]string{true: "you now run as administrator", false: "you no longer run as administrator"}[a.Permissions.FullAccess])
	}
	changes = append(changes, also...)
	if len(changes) == 0 {
		return
	}
	now := time.Now()
	if last, ok := briefed.Load(a.ID); ok && now.Sub(last.(time.Time)) < briefGap {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	go func() {
		if err := s.cfg.Chat.Brief(ctx, a, chat.RoleChange{Changes: changes}); err == nil {
			briefed.Store(a.ID, now)
		}
	}()
}

func capsOf(a storage.Agent) []string {
	c := slices.Clone(perm.Resolve(a, "", perm.Policy{MaxLevel: perm.Operate}).Caps)
	slices.Sort(c)
	return c
}
