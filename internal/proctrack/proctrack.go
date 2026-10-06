// Package proctrack remembers what each process office starts is for (an
// agent's answer, an automation's script), so the dashboard's process list
// can say it and stop it the right way.
package proctrack

import (
	"context"
	"sync"
)

// Info is what a process works for.
type Info struct {
	Kind           string `json:"kind"` // agent | automation
	TurnID         string `json:"turn_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	AutomationID   string `json:"automation_id,omitempty"`
	Label          string `json:"label"` // the agent's or the automation's name
}

type key struct{}

// With carries info to the processes started under ctx.
func With(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, key{}, info)
}

// From is the info ctx carries.
func From(ctx context.Context) (Info, bool) {
	info, ok := ctx.Value(key{}).(Info)
	return info, ok
}

var (
	mu  sync.Mutex
	reg = map[int]Info{}
)

// Track remembers pid as started under ctx (nothing when ctx says nothing);
// call what it returns once the process is done.
func Track(ctx context.Context, pid int) func() {
	info, ok := From(ctx)
	if !ok || pid <= 0 {
		return func() {}
	}
	mu.Lock()
	reg[pid] = info
	mu.Unlock()
	return func() {
		mu.Lock()
		delete(reg, pid)
		mu.Unlock()
	}
}

// Lookup is what pid was started for.
func Lookup(pid int) (Info, bool) {
	mu.Lock()
	defer mu.Unlock()
	info, ok := reg[pid]
	return info, ok
}
