package transfer_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/features"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/transfer"
	"github.com/tung1998/agent-office/templates/packs"
)

// ADR-199: every pack of office's library imports whole into an empty office:
// its agents, workflows, its one coordination schedule (off), its Burn with its review profile (off; the startup has none),
// nothing skipped. A pack that breaks (a workflow that does not read, a
// schedule that does not check) fails here, not on a person's machine.
func TestLibraryPacksImport(t *testing.T) {
	ctx := context.Background()
	list, err := packs.Bundles()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, b := range list {
		for _, e := range b.Packs {
			raw, err := packs.Read(e.Ref)
			if err != nil {
				t.Fatal(err)
			}
			p, err := transfer.ReadPack(raw)
			if err != nil {
				t.Fatalf("%s: %v", e.Ref, err)
			}
			o := newOffice(t)
			o.tr.SetCollections(collections.New(o.st))
			o.tr.SetBurnCheck(burn.CheckSetup)
			repo, _ := o.st.Repos().Create(ctx, storage.Repo{Name: "x", Path: t.TempDir()})
			res, err := o.tr.ImportPack(ctx, p, repo, p.Include)
			if err != nil {
				t.Fatalf("%s: %v", e.Ref, err)
			}
			for _, c := range res.Changes {
				if c.Op == "skip" {
					t.Errorf("%s: %s %s skipped: %s", e.Ref, c.Kind, c.Name, c.Detail)
				}
			}
			agents, _ := o.st.Agents().List(ctx, repo.ID)
			wfs, _ := o.st.Workflows().List(ctx, repo.ID)
			autos, _ := o.st.Automations().List(ctx, repo.ID)
			// the rules of an organisation (coordination, Burn) are the startup's;
			// another bundle (a learner's, ngoai-ngu) only imports whole, off
			company := b.Name == "@agentoffice/startup"
			// ADR-204: the startup starts with its CEO alone, it makes the rest
			alone := e.Ref == "@agentoffice/startup"
			if alone && (len(agents) != 1 || agents[0].Key != "ceo" || !slices.Contains(*agents[0].Permissions.Caps, perm.CapSelf)) {
				t.Fatalf("%s: agents %+v", e.Ref, agents)
			}
			if !alone && len(agents) < 3 || len(wfs) < 3 {
				t.Fatalf("%s: %d agents, %d workflows", e.Ref, len(agents), len(wfs))
			}
			// ADR-200: a coordination schedule (and the board's morning report,
			// ADR-210), off until its head is given work
			if len(autos) == 0 || company && (len(autos) > 2 || !strings.HasPrefix(autos[0].Name, "Điều phối")) {
				t.Fatalf("%s: schedules %+v", e.Ref, autos)
			}
			for _, a := range autos {
				if a.Enabled {
					t.Fatalf("%s: %s is on", e.Ref, a.Name)
				}
			}
			// ADR-203: the head knows it coordinates, from its instructions: it sets
			// up the work (project, team, workflow) and keeps it in check
			for _, a := range agents {
				// the CEO directs a project, the project runs itself (its own team, workflows)
				want := map[string]string{"ceo": "#nhan-viec", "truong-don-vi": "#nhan-viec"}[a.Key]
				if want != "" && (!strings.Contains(a.Instructions, "## Bạn là ai") || !strings.Contains(a.Instructions, want)) {
					t.Fatalf("%s: %s does not know what it is", e.Ref, a.Key)
				}
			}
			// every agent of a pack comes from the agent library: the same,
			// or the library's with a section of its own added at the end
			for _, a := range agents {
				lib, err := team.LibraryAgentByID("office/" + a.Key)
				if err != nil {
					t.Fatalf("%s: %s is not in the agent library", e.Ref, a.Key)
				}
				if !strings.HasPrefix(a.Instructions, lib.Instructions) || a.ModelTier != lib.ModelTier || perm.Agent(a) != lib.Level ||
					!slices.Equal(capsOf(a.Permissions), capsOf(lib.Permissions)) {
					t.Fatalf("%s: %s differs from office/%s of the library", e.Ref, a.Key, a.Key)
				}
			}
			bs, err := o.st.Burn().Session(ctx, repo.ID)
			if company && !alone && (err != nil || bs.ReviewProfileID == "" || bs.Active()) {
				t.Fatalf("%s: burn %+v %v", e.Ref, bs, err)
			}
			// a learner: notebook tables, its five workflows, three schedules off
			if b.Name == "@agentoffice/ngoai-ngu" {
				cols, _ := o.st.Collections().List(ctx, repo.ID)
				if len(agents) != 3 || len(wfs) != 5 || len(autos) != 3 || len(cols) < 4 {
					t.Fatalf("ngoai-ngu: %d agents, %d workflows, %d schedules, %d tables", len(agents), len(wfs), len(autos), len(cols))
				}
			}
			if e.Key == "cskh" {
				if fc := features.LoadConfig(ctx, o.st, repo.ID); !fc.Packs["support"] {
					t.Fatalf("cskh: support pack off: %+v", fc)
				}
			}
			n++
		}
	}
	if n < 8 {
		t.Fatalf("only %d packs in the library", n)
	}
}

// A unit from the library by name (what propose_create_project does): a new
// repo with the pack and its skills, committed; its agents; linked to its parent.
func TestCreateUnitFromLibrary(t *testing.T) {
	ctx := context.Background()
	o := newOffice(t)
	o.tr.SetCollections(collections.New(o.st))
	o.tr.SetBurnCheck(burn.CheckSetup)
	o.tr.SetCloneRoot(t.TempDir())
	hq, _ := o.st.Repos().Create(ctx, storage.Repo{Name: "hq", Path: t.TempDir()})
	np := storage.NewProject{Name: "Marketing", Pack: "@agentoffice/startup/marketing", Parent: hq.ID}
	if err := o.tr.CheckNewProject(ctx, &np); err != nil {
		t.Fatal(err)
	}
	r, err := o.tr.CreateProject(ctx, np)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Path, ".claude/skills/van-hanh/SKILL.md")); err != nil {
		t.Fatal("the unit's skills are not in its repo")
	}
	agents, _ := o.st.Agents().List(ctx, r.ID)
	if len(agents) != 3 || r.Name != "Marketing" {
		t.Fatalf("unit: %s, %d agents", r.Name, len(agents))
	}
	if !slices.Contains(perm.LoadPolicy(ctx, o.st, hq.ID).LinkedProjects, r.ID) {
		t.Fatal("not linked to its parent")
	}
	bad := storage.NewProject{Name: "x", Pack: "@agentoffice/startup/marketing", URL: "https://github.com/a/b"}
	if err := o.tr.CheckNewProject(ctx, &bad); err == nil {
		t.Fatal("a library pack with a url to clone was accepted")
	}
}

func capsOf(p storage.Permissions) []string {
	if p.Caps == nil {
		return nil
	}
	return *p.Caps
}

// ADR-206: a project made from a template is brought up to a newer version of
// it: what the project changed in the template's settings goes back, what it
// lacks is made, what it added is kept, its administrator rights and the
// state of its schedules stay; a second sync has nothing left to do.
func TestSyncPack(t *testing.T) {
	ctx := context.Background()
	raw, err := packs.Read("@agentoffice/startup/giam-sat")
	if err != nil {
		t.Fatal(err)
	}
	p, err := transfer.ReadPack(raw)
	if err != nil {
		t.Fatal(err)
	}
	o := newOffice(t)
	o.tr.SetCollections(collections.New(o.st))
	o.tr.SetBurnCheck(burn.CheckSetup)
	repo, _ := o.st.Repos().Create(ctx, storage.Repo{Name: "x", Path: t.TempDir()})
	if _, err := o.tr.ImportPack(ctx, p, repo, p.Include); err != nil {
		t.Fatal(err)
	}
	agents, _ := o.st.Agents().List(ctx, repo.ID)
	for _, a := range agents {
		switch a.Key {
		case "ceo": // drifted, and made administrator by a person
			a.Instructions, a.Permissions.FullAccess, a.Permissions.FullAccessBy = "cũ", true, "boss@x.io"
			o.st.Agents().Update(ctx, a)
		case "nghien-cuu": // deleted by the person
			o.st.Agents().Delete(ctx, a.ID)
		}
	}
	o.st.Agents().Create(ctx, storage.Agent{ProjectID: repo.ID, Key: "rieng", Name: "Riêng", ModelTier: storage.TierFast})
	autos, _ := o.st.Automations().List(ctx, repo.ID)
	autos[0].Enabled, autos[0].Prompt = true, "cũ"
	o.st.Automations().Update(ctx, autos[0])
	wf, _ := o.st.Workflows().List(ctx, repo.ID)
	o.st.Workflows().Delete(ctx, wf[0].ID)

	ops := func(res transfer.Result) map[string]string {
		out := map[string]string{}
		for _, c := range res.Changes {
			out[c.Kind+" "+c.Name] = c.Op
		}
		return out
	}
	preview, err := o.tr.SyncPack(ctx, p, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	got := ops(preview)
	if got["agent Tổng giám đốc"] != "update" || got["agent Nghiên cứu"] != "create" || got["agent Riêng"] != "unchanged" ||
		got["agent Thực thi"] != "unchanged" || got["automation Điều phối"] != "update" || got["workflow "+wf[0].Key] != "create" {
		t.Fatalf("preview: %v", got)
	}
	if a, _ := o.st.Agents().Get(ctx, agents[0].ID); a.Key == "ceo" && a.Instructions != "cũ" {
		t.Fatal("a preview changed the project")
	}
	if _, err := o.tr.SyncPack(ctx, p, repo, false); err != nil {
		t.Fatal(err)
	}
	after, _ := o.st.Agents().List(ctx, repo.ID)
	keys := map[string]storage.Agent{}
	for _, a := range after {
		keys[a.Key] = a
	}
	if keys["ceo"].Instructions == "cũ" || !keys["ceo"].Permissions.FullAccess || keys["nghien-cuu"].ID == "" || keys["rieng"].ID == "" {
		t.Fatalf("after: %+v", keys)
	}
	if a, _ := o.st.Automations().Get(ctx, autos[0].ID); a.Prompt == "cũ" || !a.Enabled {
		t.Fatalf("schedule: %+v", a)
	}
	again, _ := o.tr.SyncPack(ctx, p, repo, true)
	for _, c := range again.Changes {
		if c.Op != "unchanged" {
			t.Fatalf("a second sync still changes %+v", c)
		}
	}
}
