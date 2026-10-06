package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// officeExecutor runs automation jobs as chats and tasks (ADR-040). Agents
// work within their own permissions: the mode sets no extra ceiling.
type officeExecutor struct {
	chat *chat.Engine
}

func (x officeExecutor) RunChat(ctx context.Context, projectID, agentID, conversationID, prompt, editMode string) (string, string, error) {
	ctx = chat.WithModelTier(ctx, trigger.ModelTierOf(ctx)) // the automation's model choice
	ctx = chat.WithSkill(chat.WithInstructions(ctx, trigger.InstructionsOf(ctx)), trigger.SkillOf(ctx))
	if trigger.FullAccessOf(ctx) { // a bot's admin with an admin agent, or an automation's override (ADR-074, ADR-081)
		ctx = chat.WithFullAccess(ctx)
	}
	if c := trigger.CeilingOf(ctx); c != "" { // a bot's Người dùng: no higher than proposing (ADR-081)
		ctx = chat.WithCeiling(ctx, c)
	}
	if d, ok := trigger.TimeLimitOf(ctx); ok { // the automation's own limit (0: none, ADR-082)
		ctx = chat.WithTurnTimeout(ctx, d)
	}
	// extra read dirs: internal/chat.Engine reads them straight off the job
	// (computed once when it was created, ADR-074 security fix) — not from ctx.
	untrusted := trigger.UntrustedOf(ctx)
	if untrusted { // outsiders drove it (a bot's escalation): no tools, read only
		ctx = chat.WithNoTools(ctx)
	}
	if conversationID != "" { // a chat kept across runs, whose data was cleaned since: a new one
		if c, err := x.chat.Conversation(ctx, conversationID); err == nil && c.Cleaned != "" {
			conversationID = ""
		}
	}
	if conversationID == "" {
		purpose, mode := "", perm.Operate
		if untrusted {
			purpose, mode = "channel", perm.Read
		}
		conv, err := x.chat.StartConversationPurpose(ctx, projectID, agentID, purpose)
		if err != nil {
			return "", "", err
		}
		conversationID = conv.ID
		if err := x.chat.SetMode(ctx, conv.ID, mode); err != nil {
			return conversationID, "", err
		}
		if editMode != "" {
			if err := x.chat.SetEditMode(ctx, conv.ID, editMode); err != nil {
				return conversationID, "", err
			}
		}
	}
	if err := x.chat.AddTags(ctx, conversationID, trigger.TagsOf(ctx)); err != nil { // the automation's tags, before it talks
		slog.Warn("automation: chat tags", "conversation", conversationID, "err", err)
	}
	turn, _, err := x.chat.Send(ctx, conversationID, prompt, trigger.AttachmentsOf(ctx)) // with the files of a bot's message
	if errors.Is(err, chat.ErrBusy) {
		return conversationID, "", trigger.ErrBusy
	}
	var off *chat.OffError
	if errors.As(err, &off) { // tagged only paused agents: the notice is the answer, no AI ran
		return conversationID, off.Notice, nil
	}
	if err != nil {
		return conversationID, "", err
	}
	reply, replyID, author := "", "", ""
	for seq := 0; ; {
		evs, done, wake := turn.Since(seq)
		seq += len(evs)
		progress := trigger.ProgressOf(ctx)
		for _, e := range evs {
			if e.Type == "error" {
				return conversationID, "", errors.New(e.Text)
			}
			if progress != nil && e.Type == "tool" && e.Tool != nil && e.Tool.Summary != "" { // a bot's chat sees the steps
				progress(e.Tool.Summary)
			}
			if e.Type == "done" && e.Message != nil {
				reply, replyID, author = e.Message.Content, e.Message.ID, e.Message.Author
			}
		}
		if done {
			// the agents tagged in the message answer after it, each on its own
			// message, named so the chat sees who says what
			names := map[string]bool{author: true}
			if agents, err := x.chat.Agents(ctx, projectID); err == nil {
				for _, a := range chat.Mentions(prompt, agents) {
					names[a.Name] = true
				}
			}
			named := len(names) > 1
			if named && reply != "" {
				reply = signed(author, reply)
			}
			if fn := trigger.FollowUpOf(ctx); fn != nil && replyID != "" {
				go x.followUps(conversationID, replyID, names, named, fn)
			}
			return conversationID, reply, nil
		}
		select {
		case <-wake:
		case <-ctx.Done():
			turn.Cancel()
			return conversationID, "", ctx.Err()
		}
	}
}

// assistantRunner starts what the office assistant proposed and a person
// approved (ADR-046).
type assistantRunner struct {
	store   storage.Store
	trigger *trigger.Runner
}

func (r assistantRunner) RunAutomation(ctx context.Context, automationID string) (string, error) {
	a, err := r.store.Automations().Get(ctx, automationID)
	if err != nil {
		return "", err
	}
	if trigger.IsChannel(a.Source) {
		return "", errors.New("tự động hóa này chạy khi có tin nhắn tới bot, không chạy tay được")
	}
	j, _, err := r.trigger.Enqueue(ctx, a, "manual", "", "", "")
	return j.ID, err
}

// signed puts who answers at the top of a message sent to a chat.
func signed(author, text string) string { return author + ":\n" + text }

// followUps sends on what the agents of the message say after its answer —
// the others tagged, and the answering one's reports once the agents it gave
// work to are done — until the chat is quiet.
func (x officeExecutor) followUps(convID, afterID string, names map[string]bool, named bool, send func(string)) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	seen := map[string]bool{}
	past := false // messages up to the answer were sent already
	quiet := 0
	for quiet < 2 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		if len(x.chat.Running(convID)) == 0 {
			quiet++
		} else {
			quiet = 0
		}
		msgs, err := x.chat.History(ctx, convID)
		if err != nil {
			return
		}
		past = false
		for _, m := range msgs {
			if m.ID == afterID {
				past = true
				continue
			}
			if !past || seen[m.ID] {
				continue
			}
			if (m.Role == "assistant" && names[m.Author]) || m.Role == "error" {
				seen[m.ID] = true
				if strings.TrimSpace(m.Content) == "" {
					continue
				}
				if named && m.Role == "assistant" {
					send(signed(m.Author, m.Content))
				} else {
					send(m.Content)
				}
			}
			if m.Role == "user" { // the person wrote again: that turn answers on its own
				return
			}
		}
	}
}
