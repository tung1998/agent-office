package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/storage"
)

// `office tools update` run while office runs (the weekly script automation,
// or a person in a terminal) goes through the running office (ADR-259): the
// command leaves a request in the store, office takes it and updates as the
// dashboard does (when no run uses the CLI, checked after, the old version
// put back when it does not start), and the command waits for how it ended.
// A second manager in another process would replace a CLI's files under a
// run and know nothing of the versions put back. Nobody taking the request
// means no office runs: the command then updates on its own, as before.

// cliRequestKey holds the requests not ended yet.
const cliRequestKey = "system.cli_update_requests"

type cliRequest struct {
	ID    string   `json:"id"`
	Tools []string `json:"tools,omitempty"` // empty: every CLI installed
	By    string   `json:"by"`
	Now   bool     `json:"now,omitempty"` // update now, not when idle
	At    time.Time
	Taken *time.Time  `json:"taken,omitempty"` // office took it
	Done  bool        `json:"done,omitempty"`  // every update ended
	Res   []cliResult `json:"results,omitempty"`
}

// cliResult is how one CLI's update goes: State as clitools.UpgradeView's,
// or failed / skipped when it never started.
type cliResult struct {
	Tool    string    `json:"tool"`
	State   string    `json:"state"`
	Text    string    `json:"text"`
	Started time.Time `json:"started"`
	Done    bool      `json:"done"`
}

// OK: the update ended well (or there was nothing to do).
func (r cliResult) OK() bool { return r.State == "done" || r.State == "skipped" }

// editRequests changes the requests in one write: fn gets them freshly read
// on each try (another process may write between) and says whether it
// changed them.
func editRequests(ctx context.Context, st storage.Store, fn func([]cliRequest) ([]cliRequest, bool)) ([]cliRequest, error) {
	for range 20 {
		raw, ok, err := st.Settings().GetRaw(ctx, cliRequestKey)
		if err != nil {
			return nil, err
		}
		var list []cliRequest
		if ok && raw != "" {
			if err := json.Unmarshal([]byte(raw), &list); err != nil {
				list = nil // unreadable: started over
			}
		}
		next, changed := fn(list)
		if !changed {
			return list, nil
		}
		if !ok {
			raw = ""
		}
		switch err := st.Settings().SetIf(ctx, cliRequestKey, raw, next); {
		case errors.Is(err, storage.ErrConflict):
			continue
		case err != nil:
			return nil, err
		}
		return next, nil
	}
	return nil, errors.New("không ghi được yêu cầu cập nhật CLI (ghi trùng liên tục)")
}

// requestCLIUpdate asks the running office to update tools (none = every
// CLI installed) and waits until each update ended; progress is told each
// result that changed. taken false: no office took it in takeWait, the
// request is taken back and nothing was done.
func requestCLIUpdate(ctx context.Context, st storage.Store, tools []string, by string, now bool, takeWait, poll time.Duration, progress func(cliResult)) ([]cliResult, bool, error) {
	id := fmt.Sprintf("clu_%d", time.Now().UnixNano())
	at := time.Now().UTC()
	if _, err := editRequests(ctx, st, func(l []cliRequest) ([]cliRequest, bool) {
		return append(l, cliRequest{ID: id, Tools: tools, By: by, Now: now, At: at}), true
	}); err != nil {
		return nil, false, err
	}
	seen := map[string]string{}
	for {
		var list []cliRequest
		_, _ = st.Settings().Get(ctx, cliRequestKey, &list)
		i := slices.IndexFunc(list, func(r cliRequest) bool { return r.ID == id })
		if i < 0 {
			return nil, false, errors.New("yêu cầu cập nhật CLI đã bị xóa")
		}
		r := list[i]
		if progress != nil {
			for _, res := range r.Res {
				if key := res.State + "|" + res.Text; seen[res.Tool] != key {
					seen[res.Tool] = key
					progress(res)
				}
			}
		}
		if r.Done {
			_, _ = editRequests(ctx, st, func(l []cliRequest) ([]cliRequest, bool) {
				return slices.DeleteFunc(l, func(x cliRequest) bool { return x.ID == id }), true
			})
			return r.Res, true, nil
		}
		if r.Taken == nil && time.Since(at) >= takeWait {
			// no office took it: taken back, unless it was taken meanwhile
			taken := false
			if _, err := editRequests(ctx, st, func(l []cliRequest) ([]cliRequest, bool) {
				j := slices.IndexFunc(l, func(x cliRequest) bool { return x.ID == id })
				if j < 0 || l[j].Taken != nil {
					taken = j >= 0
					return l, false
				}
				return slices.Delete(l, j, j+1), true
			}); err != nil {
				return nil, false, err
			}
			if !taken {
				return nil, false, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, true, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// cliRequestServer is office's side: it takes the requests and follows
// their updates.
type cliRequestServer struct {
	st      storage.Store
	m       *clitools.Manager
	started map[string]bool // requests this office started
}

// serveCLIRequests takes `office tools update` requests every interval.
func serveCLIRequests(ctx context.Context, st storage.Store, m *clitools.Manager, every time.Duration) {
	s := &cliRequestServer{st: st, m: m, started: map[string]bool{}}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// cliRequestKeep is how long a request is kept with nobody waiting on it
// (the command was stopped): an update when idle waits at most 12h.
const cliRequestKeep = clitools.UpgradeMaxWait + 2*time.Hour

func (s *cliRequestServer) once(ctx context.Context) {
	now := time.Now().UTC()
	// take what no office took yet, and what a previous office took but
	// never started (it stopped in between)
	var take []cliRequest
	if _, err := editRequests(ctx, s.st, func(l []cliRequest) ([]cliRequest, bool) {
		take = take[:0]
		changed := false
		l = slices.DeleteFunc(l, func(r cliRequest) bool {
			old := now.Sub(r.At) > cliRequestKeep
			changed = changed || old
			return old
		})
		for i := range l {
			r := &l[i]
			if r.Done || s.started[r.ID] || r.Taken != nil && r.Res != nil {
				continue
			}
			if r.Taken == nil {
				r.Taken, changed = &now, true
			}
			take = append(take, *r)
		}
		return l, changed
	}); err != nil {
		slog.Warn("cli update request: take", "err", err)
		return
	}
	started := map[string][]cliResult{}
	for _, r := range take {
		s.started[r.ID] = true
		started[r.ID] = s.begin(ctx, r)
	}
	// write what started and how each update goes
	if _, err := editRequests(ctx, s.st, func(l []cliRequest) ([]cliRequest, bool) {
		changed := false
		for i := range l {
			r := &l[i]
			if res, ok := started[r.ID]; ok {
				r.Res, changed = res, true
			}
			if r.Done || r.Taken == nil || r.Res == nil || !s.started[r.ID] {
				continue
			}
			done := true
			for j := range r.Res {
				x := &r.Res[j]
				if !x.Done {
					if u, ok := s.m.Upgrade(x.Tool); ok && !u.Started.Before(x.Started) {
						y := cliResult{Tool: x.Tool, State: u.State, Text: u.Summary(s.name(ctx, x.Tool)), Started: x.Started, Done: !u.Active()}
						if y != *x {
							*x, changed = y, true
						}
					}
				}
				done = done && x.Done
			}
			if done {
				r.Done, changed = true, true
			}
		}
		return l, changed
	}); err != nil {
		slog.Warn("cli update request: results", "err", err)
	}
}

func (s *cliRequestServer) name(ctx context.Context, tool string) string {
	if st, err := s.m.Status(ctx, tool); err == nil && st.Name != "" {
		return st.Name
	}
	return tool
}

// begin starts r's updates: every CLI installed when it names none (one
// office cannot update is left out), else those it names (one unknown or
// not installed fails).
func (s *cliRequestServer) begin(ctx context.Context, r cliRequest) []cliResult {
	all := len(r.Tools) == 0
	ids := r.Tools
	if all {
		ids = s.m.IDs()
	}
	out := []cliResult{}
	for _, id := range ids {
		fail := func(why string) {
			out = append(out, cliResult{Tool: id, State: "failed", Text: id + ": " + why, Done: true})
		}
		installed, _, err := s.m.Detect(ctx, id)
		switch {
		case err != nil:
			fail(fmt.Sprintf("không có CLI này (có: %v)", s.m.IDs()))
			continue
		case !installed:
			if !all {
				fail("chưa cài")
			}
			continue
		}
		var u clitools.UpgradeView
		if r.Now {
			u, err = s.m.UpdateNow(id, r.By)
		} else {
			u, err = s.m.UpdateWhenIdle(id, r.By)
		}
		switch {
		case errors.Is(err, clitools.ErrUpgrading): // one on its way: followed
		case errors.Is(err, clitools.ErrUnknownInstall) && all:
			out = append(out, cliResult{Tool: id, State: "skipped", Text: id + ": bỏ qua, " + err.Error(), Done: true})
			continue
		case err != nil:
			fail(err.Error())
			continue
		}
		out = append(out, cliResult{Tool: id, State: u.State, Text: u.Summary(s.name(ctx, id)), Started: u.Started, Done: !u.Active()})
	}
	return out
}
