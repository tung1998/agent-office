package embed

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Index keeps each project's notes embedded. Like the Cognee sync (ADR-195)
// it compares rather than hooks: each round only notes new or edited since
// are embedded, those gone are dropped, a new model re-embeds all.
type Index struct {
	store   storage.Store
	resolve func(ctx context.Context) *Client
	mu      sync.Mutex
	client  *Client // nil = off
}

func New(store storage.Store, resolve func(ctx context.Context) *Client) *Index {
	return &Index{store: store, resolve: resolve}
}

const batch = 64 // texts per call

func stateKey(projectID string) string { return "embed_index/" + projectID }

// state is a project's vectors, by note id.
type state struct {
	Model string           `json:"model"`
	Notes map[string]entry `json:"notes"`
}

type entry struct {
	Hash string `json:"h"`
	Text string `json:"t"` // "[agent · topic] note", what a hit shows
	Vec  string `json:"v"` // float32 little endian, base64
}

func (s *Index) current() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// On: the notes are embedded (a connection or server is set).
func (s *Index) On() bool { return s.current() != nil }

// Run embeds every project now, then every interval, until ctx ends.
func (s *Index) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.All(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// All brings every project up to its notes; a failed one is tried again
// next round.
func (s *Index) All(ctx context.Context) {
	if s.resolve != nil {
		c := s.resolve(ctx)
		s.mu.Lock()
		s.client = c
		s.mu.Unlock()
	}
	if s.current() == nil {
		return
	}
	repos, err := s.store.Repos().List(ctx)
	if err != nil {
		return
	}
	for _, r := range repos {
		if err := s.Project(ctx, r.ID); err != nil {
			slog.Warn("embed: sync", "project", r.ID, "err", err)
		}
	}
}

// Project embeds the project's notes not embedded yet (or edited since) and
// drops those gone; saved after each batch, so a failure keeps what is done.
func (s *Index) Project(ctx context.Context, projectID string) error {
	client := s.current()
	if client == nil {
		return nil
	}
	docs, err := notes(ctx, s.store, projectID)
	if err != nil {
		return err
	}
	st := s.load(ctx, projectID)
	changed := false
	if st.Model != client.Model {
		st = state{Model: client.Model, Notes: map[string]entry{}}
		changed = true
	}
	live := map[string]bool{}
	var todo []doc
	for _, d := range docs {
		live[d.id] = true
		if e, ok := st.Notes[d.id]; !ok || e.Hash != hash(d.text) {
			todo = append(todo, d)
		}
	}
	for id := range st.Notes {
		if !live[id] {
			delete(st.Notes, id)
			changed = true
		}
	}
	for len(todo) > 0 {
		part := todo[:min(batch, len(todo))]
		todo = todo[len(part):]
		texts := make([]string, len(part))
		for i, d := range part {
			texts[i] = d.text
		}
		vecs, err := client.Embed(ctx, texts)
		if err != nil {
			if changed {
				_ = s.store.Settings().Set(ctx, stateKey(projectID), st)
			}
			return err
		}
		for i, d := range part {
			st.Notes[d.id] = entry{Hash: hash(d.text), Text: d.text, Vec: encode(vecs[i])}
		}
		changed = true
		if err := s.store.Settings().Set(ctx, stateKey(projectID), st); err != nil {
			return err
		}
	}
	if changed {
		return s.store.Settings().Set(ctx, stateKey(projectID), st)
	}
	return nil
}

func (s *Index) load(ctx context.Context, projectID string) state {
	var st state
	_, _ = s.store.Settings().Get(ctx, stateKey(projectID), &st)
	if st.Notes == nil {
		st.Notes = map[string]entry{}
	}
	return st
}

// minScore: a note less alike than this is not shown.
const minScore = 0.25

// Related is the project's notes nearest query by meaning, of every agent
// ("[agent · topic] note" each), leaving out those skip already shows;
// nothing when off or nothing is embedded yet.
func (s *Index) Related(ctx context.Context, projectID, query, skip string, n int) ([]string, error) {
	client := s.current()
	if client == nil {
		return nil, nil
	}
	st := s.load(ctx, projectID)
	if st.Model != client.Model || len(st.Notes) == 0 {
		return nil, nil
	}
	q, err := client.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	type scored struct {
		text  string
		score float64
	}
	var all []scored
	for _, e := range st.Notes {
		if _, note, ok := strings.Cut(e.Text, "] "); ok && strings.Contains(skip, note) {
			continue
		}
		if sc := cosine(q[0], decode(e.Vec)); sc >= minScore {
			all = append(all, scored{e.Text, sc})
		}
	}
	slices.SortFunc(all, func(a, b scored) int { return cmp.Or(cmp.Compare(b.score, a.score), strings.Compare(a.text, b.text)) })
	var out []string
	for _, h := range all[:min(n, len(all))] {
		out = append(out, h.text)
	}
	return out, nil
}

// doc is one note as it is embedded: who wrote it and its topic, then the
// note, so a hit reads on its own.
type doc struct{ id, text string }

func notes(ctx context.Context, st storage.Store, projectID string) ([]doc, error) {
	agents, err := st.Agents().List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var out []doc
	for _, a := range agents {
		list, err := st.Memories().List(ctx, projectID, a.ID)
		if err != nil {
			return nil, err
		}
		name := cmp.Or(a.Name, a.Key, a.ID)
		for _, m := range list {
			out = append(out, doc{id: m.ID, text: "[" + name + " · " + cmp.Or(m.Topic, "core") + "] " + m.Text})
		}
	}
	return out, nil
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func encode(v []float32) string {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func decode(s string) []float32 {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// Count is how many of the project's notes are embedded with model.
func (s *Index) Count(ctx context.Context, projectID, model string) int {
	st := s.load(ctx, projectID)
	if st.Model != model {
		return 0
	}
	return len(st.Notes)
}
