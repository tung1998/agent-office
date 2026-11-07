package support

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// The pack's agent tools (ADR-174), only where the pack is on: playbooks
// searched with their source, tickets read, a note or a case set.

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// Tools lists the pack's tools for sc (none where the pack is off).
func (s *Service) Tools(sc officetools.Scope) []officetools.Tool {
	if !s.On(context.Background(), sc.ProjectID) {
		return nil
	}
	out := []officetools.Tool{
		{Name: "support_kb_search", ReadOnly: true,
			Description: "Search the project's playbooks (documents, files, links, canned answers) for how to answer a customer: the best matches with their title and source. Quote the source you use.",
			Schema:      obj(map[string]any{"query": strProp("What the customer wants, in a few words"), "case_type": strProp("Only playbooks of this case type (optional)")}, "query")},
		{Name: "support_ticket_get", ReadOnly: true, Description: "A support ticket: its customer, case, status and whole history (messages, replies, notes).",
			Schema: obj(map[string]any{"ticket": strProp("Ticket id")}, "ticket")},
		{Name: "support_tickets_list", ReadOnly: true, Description: "The project's latest support tickets (optionally of one status: new open waiting_customer waiting_approval resolved closed).",
			Schema: obj(map[string]any{"status": strProp("Status (optional)")})},
	}
	if perm.AtLeast(sc.Level, perm.Propose) {
		out = append(out,
			officetools.Tool{Name: "support_note", Description: "Add an internal note to a ticket (the customer never sees it).",
				Schema: obj(map[string]any{"ticket": strProp("Ticket id"), "text": strProp("The note")}, "ticket", "text")},
			officetools.Tool{Name: "support_set", Description: "Set a ticket's case type (a key of the project's case types) and/or tags.",
				Schema: obj(map[string]any{"ticket": strProp("Ticket id"), "case_type": strProp("Case type key"),
					"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags to add"}}, "ticket")})
	}
	return out
}

// Call runs one of the pack's tools.
func (s *Service) Call(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, bool) {
	var in struct {
		Query    string   `json:"query"`
		CaseType string   `json:"case_type"`
		Ticket   string   `json:"ticket"`
		Status   string   `json:"status"`
		Text     string   `json:"text"`
		Tags     []string `json:"tags"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "Tham số không đọc được: " + err.Error(), true
		}
	}
	if !s.On(ctx, sc.ProjectID) {
		return "Gói Chăm sóc khách hàng chưa bật.", true
	}
	ticket := func() (storage.SupportTicket, error) {
		t, err := s.store.Support().Ticket(ctx, in.Ticket)
		if err != nil || t.ProjectID != sc.ProjectID {
			return t, fmt.Errorf("không có ticket %q", in.Ticket)
		}
		return t, nil
	}
	switch name {
	case "support_kb_search":
		return s.kbSearch(ctx, sc.ProjectID, in.Query, in.CaseType)
	case "support_ticket_get":
		t, err := ticket()
		if err != nil {
			return err.Error(), true
		}
		evs, _ := s.store.Support().Events(ctx, t.ID)
		var lines []map[string]any
		for _, e := range evs {
			if e.Kind == "customer_msg" || e.Kind == "sent" || e.Kind == "note" {
				lines = append(lines, map[string]any{"kind": e.Kind, "by": e.Actor, "text": e.Body, "at": e.CreatedAt})
			}
		}
		out := map[string]any{"ticket": t, "history": lines}
		if t.CustomerID != "" {
			if x, _, err := s.collections.Get(ctx, t.ProjectID, "customers", t.CustomerID); err == nil {
				out["customer"] = x.Data
			}
		}
		return toJSON(out), false
	case "support_tickets_list":
		list, err := s.store.Support().Tickets(ctx, sc.ProjectID, in.Status, 50)
		if err != nil {
			return err.Error(), true
		}
		var out []map[string]any
		for _, t := range list {
			out = append(out, map[string]any{"id": t.ID, "subject": t.Subject, "status": t.Status, "case_type": t.CaseType, "updated_at": t.UpdatedAt})
		}
		return toJSON(out), false
	case "support_note", "support_set":
		if !perm.AtLeast(sc.Level, perm.Propose) {
			return "Quyền hiện tại chỉ đọc.", true
		}
		t, err := ticket()
		if err != nil {
			return err.Error(), true
		}
		if name == "support_note" {
			if strings.TrimSpace(in.Text) == "" {
				return "Ghi chú trống.", true
			}
			_, err := s.store.Support().AddEvent(ctx, storage.SupportEvent{TicketID: t.ID, SourceID: t.SourceID, Kind: "note", Body: strings.TrimSpace(in.Text), Actor: "agent:" + sc.Agent})
			if err != nil {
				return err.Error(), true
			}
			s.notify(t.ID)
			return "Đã ghi chú vào ticket.", false
		}
		if in.CaseType != "" {
			if _, ok := s.LoadSettings(ctx, t.ProjectID).caseOf(in.CaseType); !ok {
				return fmt.Sprintf("Không có loại case %q.", in.CaseType), true
			}
		}
		if _, err := patchTicket(ctx, s.store.Support(), t.ID, func(x *storage.SupportTicket) {
			if in.CaseType != "" {
				x.CaseType = in.CaseType
			}
			for _, tag := range in.Tags {
				if tag = strings.TrimSpace(tag); tag != "" && tag != "human" && !contains(x.Tags, tag) {
					x.Tags = append(x.Tags, tag)
				}
			}
		}); err != nil {
			return err.Error(), true
		}
		s.notify(t.ID)
		return "Đã cập nhật ticket.", false
	}
	return "Không có công cụ " + name + ".", true
}

// kbSearch: the playbooks matching query (words, accents ignored), the
// case's own first; a playbook turned off is left out.
func (s *Service) kbSearch(ctx context.Context, projectID, query, caseType string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Cần nội dung để tìm.", true
	}
	q := collections.Query{Search: query, Limit: 8}
	if caseType != "" {
		q.Where = []storage.Cond{{Field: "case_type", Value: caseType}}
	}
	list, _, err := s.collections.Query(ctx, projectID, "playbooks", q)
	if err == nil && len(list) == 0 && caseType != "" { // nothing in the case: any
		list, _, err = s.collections.Query(ctx, projectID, "playbooks", collections.Query{Search: query, Limit: 8})
	}
	if err == nil && len(list) == 0 { // no word matched them all: any word
		var words []string
		for _, w := range strings.Fields(query) {
			if len([]rune(w)) > 2 {
				words = append(words, w)
			}
		}
		seen := map[string]bool{}
		for _, w := range words {
			more, _, _ := s.collections.Query(ctx, projectID, "playbooks", collections.Query{Search: w, Limit: 5})
			for _, x := range more {
				if !seen[x.ID] {
					seen[x.ID] = true
					list = append(list, x)
				}
			}
		}
	}
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	n := 0
	for _, x := range list {
		if on, ok := x.Data["enabled"].(bool); ok && !on {
			continue
		}
		n++
		fmt.Fprintf(&b, "## %s (%s)", str(x.Data["title"]), str(x.Data["kind"]))
		if c := str(x.Data["case_type"]); c != "" {
			fmt.Fprintf(&b, " · case %s", c)
		}
		if src := str(x.Data["source"]); src != "" {
			fmt.Fprintf(&b, "\nNguồn: %s", src)
		}
		if c := str(x.Data["content"]); c != "" {
			fmt.Fprintf(&b, "\n%s", cut(c, 1500))
		}
		fmt.Fprintf(&b, "\n(id %s)\n\n", x.ID)
	}
	if n == 0 {
		return "Không có phương án nào khớp. Nói rõ là chưa có thông tin, đừng bịa.", false
	}
	return b.String(), false
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
