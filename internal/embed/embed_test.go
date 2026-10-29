package embed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/textrank"
)

// fake embeds by concept: words of one meaning share a dimension, so
// "refund" is near "hoàn tiền" with no word in common.
type fake struct {
	mu     sync.Mutex
	inputs int
	model  string
	auth   string
	fail   bool
}

var concepts = [][]string{{"hoan", "refund", "tra lai"}, {"deploy", "trien khai", "ship"}, {"pnpm", "npm", "lockfile"}}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	f.mu.Lock()
	f.inputs += len(in.Input)
	f.model, f.auth = in.Model, r.Header.Get("Authorization")
	fail := f.fail
	f.mu.Unlock()
	if r.URL.Path != "/v1/embeddings" || fail {
		http.Error(w, `{"error":{"message":"nope"}}`, 500)
		return
	}
	type item struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}
	var data []item
	for i, t := range in.Input {
		t = textrank.Fold(t)
		v := make([]float32, len(concepts)+1)
		v[len(concepts)] = 0.1 // so no vector is all zero
		for k, words := range concepts {
			for _, w := range words {
				if strings.Contains(t, w) {
					v[k] = 1
				}
			}
		}
		data = append(data, item{i, v})
	}
	// out of order on purpose: the client puts them back by index
	for i, j := 0, len(data)-1; i < j; i, j = i+1, j-1 {
		data[i], data[j] = data[j], data[i]
	}
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.inputs
	f.inputs = 0
	return n
}

func TestIndex(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	a, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "dev", Name: "Dev", ModelTier: "fast"})
	refund, _ := st.Memories().Create(ctx, storage.Memory{ProjectID: p.ID, AgentID: a.ID, Text: "Hoàn tiền PayPal qua job refund_sync", Topic: "thanh-toan"})
	pn, _ := st.Memories().Create(ctx, storage.Memory{ProjectID: p.ID, AgentID: a.ID, Text: "Repo dùng pnpm"})

	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	model := "m1"
	off := false
	ix := New(st, func(context.Context) *Client {
		if off {
			return nil
		}
		return newClient(srv.URL+"/v1", "k", model)
	})

	// off: nothing, no call
	off = true
	ix.All(ctx)
	if got, err := ix.Related(ctx, p.ID, "refund", "", 5); got != nil || err != nil || f.count() != 0 {
		t.Fatalf("off = %v %v", got, err)
	}
	off = false
	ix.All(ctx)
	if n := f.count(); n != 2 || f.auth != "Bearer k" || f.model != "m1" {
		t.Fatalf("first sync embedded %d (%s %s)", n, f.auth, f.model)
	}
	got, err := ix.Related(ctx, p.ID, "how do we give money back (refund)?", "", 5)
	if err != nil || len(got) != 1 || got[0] != "[Dev · thanh-toan] Hoàn tiền PayPal qua job refund_sync" {
		t.Fatalf("related = %v %v", got, err)
	}
	// what the keywords already show is left out
	if got, _ = ix.Related(ctx, p.ID, "refund", "- [x] Hoàn tiền PayPal qua job refund_sync", 5); len(got) != 0 {
		t.Fatalf("skip = %v", got)
	}
	f.count()

	// unchanged: no call; edited: only that note; gone: dropped
	ix.All(ctx)
	if n := f.count(); n != 0 {
		t.Fatalf("unchanged sync embedded %d", n)
	}
	pn.Text = "Repo dùng pnpm, deploy bằng ship.sh"
	st.Memories().Update(ctx, pn)
	st.Memories().Delete(ctx, refund.ID)
	ix.All(ctx)
	if n := f.count(); n != 1 {
		t.Fatalf("edit sync embedded %d", n)
	}
	if got, _ = ix.Related(ctx, p.ID, "refund", "", 5); len(got) != 0 {
		t.Fatalf("deleted note still found: %v", got)
	}
	if got, _ = ix.Related(ctx, p.ID, "triển khai", "", 5); len(got) != 1 || !strings.Contains(got[0], "ship.sh") {
		t.Fatalf("edited = %v", got)
	}
	f.count()

	// a new model re-embeds every note; until then nothing is compared
	model = "m2"
	f.fail = true
	ix.All(ctx)
	if got, _ = ix.Related(ctx, p.ID, "triển khai", "", 5); got != nil {
		t.Fatalf("stale model = %v", got)
	}
	f.fail = false
	f.count()
	ix.All(ctx)
	if n := f.count(); n != 1 {
		t.Fatalf("model change embedded %d", n)
	}
	if _, err := newClient(srv.URL+"/bad", "", "m").Embed(ctx, []string{"x"}); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolver(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	key := func(p storage.Provider) (string, error) {
		if p.Name == "nokey" {
			return "", errors.New("no key")
		}
		return "sk-" + p.Name, nil
	}
	r := Resolver(st, key)
	if c := r(ctx); c != nil {
		t.Fatalf("no connection = %+v", c)
	}
	st.Providers().Create(ctx, storage.Provider{Name: "cli", Kind: storage.ProviderClaudeCLI, Enabled: true})
	st.Providers().Create(ctx, storage.Provider{Name: "nokey", Kind: storage.ProviderOpenAI, Enabled: true})
	if c := r(ctx); c != nil {
		t.Fatalf("CLI or keyless = %+v", c)
	}
	st.Providers().Create(ctx, storage.Provider{Name: "compat", Kind: storage.ProviderOpenAICompatible, BaseURL: "http://localhost:11434/v1/", Enabled: true})
	if c := r(ctx); c != nil {
		t.Fatalf("compatible without a model = %+v", c)
	}
	st.Providers().Create(ctx, storage.Provider{Name: "oa", Kind: storage.ProviderOpenAI, Enabled: true})
	if c := r(ctx); c == nil || c.URL != "https://api.openai.com/v1" || c.Model != DefaultModel || c.Key != "sk-oa" {
		t.Fatalf("auto = %+v", c)
	}
	Save(ctx, st, Settings{Provider: "compat", Model: "nomic-embed-text"})
	if c := r(ctx); c == nil || c.URL != "http://localhost:11434/v1" || c.Model != "nomic-embed-text" {
		t.Fatalf("chosen = %+v", c)
	}
	Save(ctx, st, Settings{Off: true})
	if c := r(ctx); c != nil {
		t.Fatalf("off = %+v", c)
	}
	t.Setenv(EnvURL, "http://emb.local/v1")
	if c := r(ctx); c == nil || c.URL != "http://emb.local/v1" || c.Model != DefaultModel {
		t.Fatalf("env = %+v", c)
	}
}
