package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/assistant"
)

// Chat tags: a person labels chats to find them again; the project's tags
// are offered back when adding one, and the chat list filters by them.

const (
	maxTagLen  = 30
	maxChatTag = 10
)

var errTags = errors.New("tag tối đa 30 ký tự, mỗi chat tối đa 10 tag")

// cleanTags trims tags, folds inner spaces and drops repeats ("Bug" = "bug").
func cleanTags(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagLen {
			return nil, errTags
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) > maxChatTag {
		return nil, errTags
	}
	return out, nil
}

// setTags replaces a chat's tags (whoever may open the chat).
func (s *server) setTags(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tags []string `json:"tags"`
	}
	if !decode(w, r, &in) {
		return
	}
	tags, err := cleanTags(in.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Store.Chat().SetConversationTags(r.Context(), r.PathValue("id"), tags); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	c, err := s.cfg.Store.Chat().GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": s.toConvDTO(c)})
}

// projectTags: the tags a project's chats use, the last used first (quick pick, filter).
func (s *server) projectTags(w http.ResponseWriter, r *http.Request) {
	who := ""
	if r.PathValue("id") == assistant.ID(r.Context(), s.cfg.Store) { // assistant chats are each person's own
		who = "human:" + userFrom(r).Email
	}
	list, err := s.cfg.Store.Chat().ProjectTags(r.Context(), r.PathValue("id"), who)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": list})
}
