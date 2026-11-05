// Package busywork lists the work a restart of office would cut off (ADR-255):
// answers being written, workflows, automation runs, Burn pieces and tasks.
// The update page shows it, an update that waits for office to be idle
// waits for it to be empty.
package busywork

import (
	"context"
	"fmt"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// Item is one piece of work in progress.
type Item struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Link      string `json:"link"` // where it is seen in the dashboard
}

// Report is the work in progress, by kind.
type Report struct {
	// Chats is the answers being written (the chat engine's count); ChatItems
	// those of them a person started, as far as the jobs tell.
	Chats       int    `json:"chats"`
	ChatItems   []Item `json:"chat_items"`
	Workflows   []Item `json:"workflows"`
	Automations []Item `json:"automations"`
	Burn        []Item `json:"burn"`
	Tasks       []Item `json:"tasks"`
}

// Total is how many pieces of work are in progress.
func (r Report) Total() int {
	return r.Chats + len(r.Workflows) + len(r.Automations) + len(r.Burn) + len(r.Tasks)
}

// Summary says it in one line ("" when nothing runs).
func (r Report) Summary() string {
	var parts []string
	if r.Chats > 0 {
		parts = append(parts, fmt.Sprintf("%d lượt chat", r.Chats))
	}
	add := func(label string, items []Item) {
		if len(items) == 0 {
			return
		}
		names := make([]string, 0, 3)
		for i, it := range items {
			if i == 3 {
				names = append(names, "…")
				break
			}
			names = append(names, it.Name)
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", len(items), label, strings.Join(names, ", ")))
	}
	add("quy trình", r.Workflows)
	add("tự động hóa", r.Automations)
	add("việc Burn", r.Burn)
	add("Việc", r.Tasks)
	return strings.Join(parts, "; ")
}

// Turns counts the answers being written (the chat engine).
type Turns interface{ ActiveTurns() int }

// Counter reads the work in progress.
type Counter struct {
	Store storage.Store
	Chat  Turns // nil: no chat engine
}

// Count lists the work in progress; a part it cannot read is left out.
func (c Counter) Count(ctx context.Context) Report {
	r := Report{ChatItems: []Item{}, Workflows: []Item{}, Automations: []Item{}, Burn: []Item{}, Tasks: []Item{}}
	if c.Chat != nil {
		r.Chats = c.Chat.ActiveTurns()
	}
	if c.Store == nil {
		return r
	}
	if jobs, err := c.Store.Jobs().List(ctx, storage.JobFilter{Status: "running", Limit: 200}); err == nil {
		for _, j := range jobs {
			name := j.Title
			if name == "" {
				name = j.ID
			}
			switch {
			case j.Origin == "automation" || j.Origin == "monitor" || j.Origin == "retry":
				link := "/projects/" + j.ProjectID + "?tab=automations"
				if j.Origin == "automation" && j.OriginID != "" {
					link = "/projects/" + j.ProjectID + "/automations/" + j.OriginID
				}
				r.Automations = append(r.Automations, Item{ID: j.ID, Name: name, ProjectID: j.ProjectID, Link: link})
			case j.Kind == "chat_turn" && j.TaskID == "":
				r.ChatItems = append(r.ChatItems, Item{ID: j.ID, Name: name, ProjectID: j.ProjectID, Link: chatLink(j.ProjectID, j.ConversationID)})
			}
		}
	}
	if runs, err := c.Store.WorkflowRuns().List(ctx, "", "", 500); err == nil {
		for _, w := range runs {
			if w.Status != storage.RunRunning {
				continue
			}
			name := w.WorkflowName
			if name == "" {
				name = w.WorkflowKey
			}
			r.Workflows = append(r.Workflows, Item{ID: w.ID, Name: name, ProjectID: w.ProjectID, Link: chatLink(w.ProjectID, w.ConversationID)})
		}
	}
	if sessions, err := c.Store.Burn().Running(ctx); err == nil {
		for _, b := range sessions {
			items, err := c.Store.Burn().Items(ctx, b.ID)
			if err != nil {
				continue
			}
			for _, it := range items {
				if it.Status == "doing" {
					r.Burn = append(r.Burn, Item{ID: it.ID, Name: it.Title, ProjectID: b.ProjectID, Link: "/projects/" + b.ProjectID + "?tab=burn"})
				}
			}
		}
	}
	if tasks, err := c.Store.Tasks().List(ctx, "", 200); err == nil {
		for _, t := range tasks {
			if t.Status == "running" {
				r.Tasks = append(r.Tasks, Item{ID: t.ID, Name: t.Title, ProjectID: t.ProjectID, Link: "/projects/" + t.ProjectID + "?tab=tasks&task=" + t.ID})
			}
		}
	}
	return r
}

func chatLink(projectID, conversationID string) string {
	if conversationID == "" {
		return "/projects/" + projectID + "?tab=chat"
	}
	return "/projects/" + projectID + "?tab=chat&c=" + conversationID
}
