package transfer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/transfer"
)

type office struct {
	st   storage.Store
	org  *orgmodel.Service
	prov *provider.Service
	tr   *transfer.Service
}

func newOffice(t *testing.T) office {
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Migrate(context.Background())
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(context.Background())
	prov := provider.NewService(st, box, llm.Options{})
	return office{st, org, prov, transfer.New(st, prov, org)}
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newOffice(t)
	key := "sk-ant-secret-key-9999"
	claude, _ := src.prov.Create(ctx, provider.Input{Name: "Claude API", Kind: storage.ProviderAnthropic, APIKey: &key})
	gpt, _ := src.prov.Create(ctx, provider.Input{Name: "GPT", Kind: storage.ProviderOpenAI, APIKeyEnv: "OPENAI_API_KEY"})
	team, _ := src.st.OrgModels().GetTemplateByKey(ctx, "team")
	repo, _ := src.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/code/shop", Description: "Shop"})
	inst, _ := src.org.ApplyToRepo(ctx, repo.ID, team.ID, false)
	agents, _ := src.st.Agents().List(ctx, inst.ID)
	agents[0].ProviderID = gpt.ID
	agents[0].Name = "Trưởng nhóm shop"
	src.org.SaveAgent(ctx, agents[0])
	src.st.Repos().Create(ctx, storage.Repo{Name: "Trợ lý máy"})
	src.org.CloneTemplate(ctx, team.ID, "team-lite", "Team gọn")
	_ = claude

	b, err := src.tr.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := transfer.WriteDir(dir, b); err != nil {
		t.Fatal(err)
	}
	all := ""
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			raw, _ := os.ReadFile(p)
			all += string(raw)
		}
		return nil
	})
	if strings.Contains(all, "secret-key") || strings.Contains(all, "api_key_enc") || strings.Contains(all, gpt.ID) {
		t.Fatal("export leaked a secret or an internal id")
	}

	read, err := transfer.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	dst := newOffice(t)
	plan, err := dst.tr.Import(ctx, read, true)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]string{}
	for _, c := range plan.Changes {
		ops[c.Kind+":"+c.Name] = c.Op
	}
	if ops["provider:Claude API"] != "skip" || ops["provider:GPT"] != "create" || ops["template:team-lite"] != "create" ||
		ops["template:team"] != "unchanged" || ops["project:shop (/code/shop)"] != "create" || ops["project:Trợ lý máy"] != "create" {
		t.Fatalf("plan = %v", ops)
	}
	if list, _ := dst.st.Repos().List(ctx); len(list) != 0 {
		t.Fatal("dry run wrote data")
	}

	if _, err := dst.tr.Import(ctx, read, false); err != nil {
		t.Fatal(err)
	}
	r, err := dst.st.Repos().GetByPath(ctx, "/code/shop")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := dst.st.OrgModels().GetForRepo(ctx, r.ID)
	dstAgents, _ := dst.st.Agents().List(ctx, m.ID)
	dstGPT := findProvider(t, dst.st, "GPT")
	if dstAgents[0].Name != "Trưởng nhóm shop" || dstAgents[0].ProviderID != dstGPT.ID || m.SourceTemplateID == "" {
		t.Fatalf("imported model = %+v agent=%+v", m, dstAgents[0])
	}

	// importing the same bundle again changes nothing
	again, _ := dst.tr.Import(ctx, read, false)
	for _, c := range again.Changes {
		if c.Op != "unchanged" && c.Op != "skip" {
			t.Fatalf("second import changed %s %s: %s", c.Kind, c.Name, c.Op)
		}
	}

	// removing a project from the bundle removes its file on the next export
	vi, _ := filepath.Glob(filepath.Join(dir, "projects", "tro-ly-may-*.json"))
	if len(vi) != 1 {
		t.Fatal("vietnamese project name should become an ascii slug")
	}
	b.Projects = b.Projects[:1]
	transfer.WriteDir(dir, b)
	files, _ := filepath.Glob(filepath.Join(dir, "projects", "*.json"))
	if len(files) != 1 {
		t.Fatalf("stale project files: %v", files)
	}
}

func findProvider(t *testing.T, st storage.Store, name string) storage.Provider {
	list, _ := st.Providers().List(context.Background())
	for _, p := range list {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("provider %s missing", name)
	return storage.Provider{}
}
