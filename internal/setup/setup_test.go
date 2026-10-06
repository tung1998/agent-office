package setup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/setup"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

const aiAnswer = "Đây là đề xuất:\n```json\n" + `{
  "description": "Storefront Nuxt bán hàng, thanh toán Stripe.",
  "template_key": "team",
  "reason": "Có frontend, thanh toán và vận hành",
  "confidence": 0.8,
  "agent_changes": [
    {"action": "update", "key": "engineer", "instructions": "Dùng pnpm, chạy pnpm test trước khi commit.", "reason": "Quy ước trong CLAUDE.md"},
    {"action": "add", "key": "graylog-reader", "name": "Graylog Reader", "tier": "worker", "reports_to": ["qa-lead"], "source": ".claude/skills/fetch-graylog-logs/SKILL.md", "reason": "Project log qua Graylog"},
    {"action": "remove", "key": "monitor", "reason": "Đã có graylog-reader"}
  ],
  "notes": ["Nên kết nối Stripe read-only"]
}` + "\n```"

func env(t *testing.T, answer string) (*setup.Assistant, storage.Store, *provider.Service, string) {
	dir := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(dir, "o.db"))
	t.Cleanup(func() { st.Close() })
	st.Migrate(context.Background())
	org := orgmodel.NewService(st)
	org.SeedBuiltins(context.Background())
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotPrompt = body["messages"].([]any)[0].(map[string]any)["content"].(string)
		out, _ := json.Marshal(map[string]any{"model": "claude-opus-5-5", "content": []map[string]string{{"type": "text", "text": answer}},
			"usage": map[string]int{"input_tokens": 900, "output_tokens": 300}})
		w.Write(out)
	}))
	t.Cleanup(srv.Close)
	provs := provider.NewService(st, box, llm.Options{})
	key := "sk-ant-test-key-0000"
	if _, err := provs.Create(context.Background(), provider.Input{Name: "Claude", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	_ = gotPrompt
	return setup.New(st, provs, org), st, provs, srv.URL
}

func TestProposeAndAccept(t *testing.T) {
	a, st, _, _ := env(t, aiAnswer)
	ctx := context.Background()
	res, err := a.Propose(ctx, "", "shop", "Project: shop\nFramework: Nuxt", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposal.TemplateKey != "team" || len(res.Proposal.Changes) != 3 || len(res.Problems) != 0 {
		t.Fatalf("result = %+v", res)
	}
	var eng, reader *orgmodel.AgentSpec
	for i, ag := range res.Template.Agents {
		switch ag.Key {
		case "engineer":
			eng = &res.Template.Agents[i]
		case "graylog-reader":
			reader = &res.Template.Agents[i]
		case "monitor":
			t.Fatal("monitor should be removed")
		}
	}
	if eng == nil || !strings.Contains(eng.Instructions, "Bối cảnh project:\nDùng pnpm") || !strings.HasPrefix(eng.Instructions, "Làm đúng task") {
		t.Fatalf("engineer = %+v", eng)
	}
	if reader == nil || !reader.Permissions.ReadOnly || reader.ModelTier != "fast" {
		t.Fatalf("reader = %+v", reader)
	}
	if res.Model != "claude-opus-5-5" || res.Usage.InputTokens != 900 {
		t.Fatalf("usage = %+v", res)
	}

	repo, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/code/shop"})
	m, err := a.Accept(ctx, repo.ID, res.Proposal.TemplateKey, res.Proposal.Changes[:2]) // user unticked "remove monitor"
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := st.Agents().List(ctx, m.ID)
	keys := map[string]bool{}
	for _, ag := range agents {
		keys[ag.Key] = true
	}
	if !keys["graylog-reader"] || !keys["monitor"] || m.RepoID != repo.ID || m.SourceTemplateID == "" {
		t.Fatalf("installed = %+v %v", m, keys)
	}
}

func TestBuildReportsProblems(t *testing.T) {
	a, _, _, _ := env(t, aiAnswer)
	_, problems, err := a.Build(context.Background(), "team", []setup.AgentChange{
		{Action: "add", Key: "orphan", Tier: "worker", Reason: "x"}, // no reports_to
	})
	if err != nil || len(problems) == 0 {
		t.Fatalf("problems = %v, %v", problems, err)
	}
	if _, _, err := a.Build(context.Background(), "nope", nil); err == nil {
		t.Fatal("unknown template must fail")
	}
}

func TestNoProvider(t *testing.T) {
	dir := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(dir, "o.db"))
	defer st.Close()
	st.Migrate(context.Background())
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	org := orgmodel.NewService(st)
	a := setup.New(st, provider.NewService(st, box, llm.Options{}), org)
	if _, err := a.Propose(context.Background(), "", "x", "", "goal"); !errors.Is(err, setup.ErrNoProvider) {
		t.Fatalf("err = %v", err)
	}
}

func TestBadJSON(t *testing.T) {
	a, _, _, _ := env(t, "Xin lỗi, tôi không chắc.")
	if _, err := a.Propose(context.Background(), "", "x", "p", ""); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("err = %v", err)
	}
}
