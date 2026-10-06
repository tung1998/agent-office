package trigger_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// GitHub and Bitbucket pull request events: opened / new commits are PRs to
// review; closed, merged, a comment are not.
func TestParsePR(t *testing.T) {
	gh := http.Header{"X-Github-Event": {"pull_request"}}
	pr, ok, _ := trigger.ParsePR(gh, []byte(`{"action":"opened","pull_request":{"number":7,"title":"Sửa thanh toán","html_url":"https://github.com/o/r/pull/7",
		"user":{"login":"binh"},"head":{"ref":"fix/pay","sha":"abc123"},"base":{"ref":"main"}}}`))
	if !ok || pr.Provider != "github" || pr.Number != "7" || pr.Source != "fix/pay" || pr.Target != "main" || pr.Head != "abc123" || pr.Author != "binh" {
		t.Fatalf("github = %+v %v", pr, ok)
	}
	if _, ok, why := trigger.ParsePR(gh, []byte(`{"action":"closed","pull_request":{"number":7}}`)); ok || why == "" {
		t.Fatal("a closed PR is reviewed")
	}
	bb := http.Header{"X-Event-Key": {"pullrequest:updated"}}
	pr, ok, _ = trigger.ParsePR(bb, []byte(`{"pullrequest":{"id":12,"title":"Admin v3","author":{"display_name":"An"},"links":{"html":{"href":"https://bitbucket.org/o/r/pull-requests/12"}},
		"source":{"branch":{"name":"feature/x"},"commit":{"hash":"def456"}},"destination":{"branch":{"name":"master"}}}}`))
	if !ok || pr.Provider != "bitbucket" || pr.Number != "12" || pr.Source != "feature/x" || pr.Target != "master" || pr.URL == "" {
		t.Fatalf("bitbucket = %+v %v", pr, ok)
	}
	if _, ok, _ := trigger.ParsePR(http.Header{"X-Event-Key": {"pullrequest:fulfilled"}}, []byte(`{"pullrequest":{"id":12}}`)); ok {
		t.Fatal("a merged PR is reviewed")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// notes records what was sent to a chat.
type notes struct {
	mu    sync.Mutex
	sent  []string
	convs []string // the chat each run talked in
}

func (n *notes) add(_ context.Context, channelID, chatID, text, convID string) {
	n.mu.Lock()
	n.sent = append(n.sent, channelID+"|"+chatID+"|"+text)
	n.convs = append(n.convs, convID)
	n.mu.Unlock()
}

// A PR webhook: office fetches both branches and gives the agent the PR's
// diff; what the agent says goes to the chat picked for the automation.
func TestPRReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	st, _ := openStore(t)
	origin, work, project := t.TempDir(), t.TempDir(), t.TempDir()
	git(t, origin, "init", "-q", "--bare", "-b", "main")
	git(t, work, "clone", "-q", origin, ".")
	os.WriteFile(filepath.Join(work, "pay.go"), []byte("package pay\n"), 0o644)
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "init")
	git(t, work, "push", "-q", "origin", "HEAD:main")
	git(t, project, "clone", "-q", origin, ".")
	git(t, work, "checkout", "-q", "-b", "fix/pay")
	os.WriteFile(filepath.Join(work, "pay.go"), []byte("package pay\n\nfunc Charge() {}\n"), 0o644)
	git(t, work, "commit", "-q", "-am", "charge")
	git(t, work, "push", "-q", "origin", "fix/pay")
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: project})

	ex := &replyExec{reply: "LGTM, thiếu test cho Charge"}
	r := trigger.New(st, ex)
	var n notes
	r.SetOnNotify(n.add)
	secret, hash := trigger.NewSecret()
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Review PR", Source: "webhook", Action: "chat", Enabled: true,
		Prompt: "Review PR {{payload.number}} ({{payload.title}}):\n{{diff}}",
		Config: storage.AutomationConfig{Auth: "bearer", SecretHash: hash, PullRequest: true, NotifyChannelID: "chn_1", NotifyChatID: "c9"}})
	srv := httptest.NewServer(r.Webhook())
	defer srv.Close()
	post := func(event, body string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/hooks/"+a.ID, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("X-GitHub-Event", event)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("pull_request", `{"action":"closed","pull_request":{"number":7}}`); code != 200 {
		t.Fatalf("closed = %d", code)
	}
	if code := post("pull_request", `{"action":"opened","pull_request":{"number":7,"title":"Sửa thanh toán","html_url":"https://x/7","user":{"login":"binh"},
		"head":{"ref":"fix/pay","sha":"abc"},"base":{"ref":"main"}}}`); code != 202 {
		t.Fatalf("opened = %d", code)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r.Wait()
		ex.mu.Lock()
		got := len(ex.prompts)
		ex.mu.Unlock()
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no review ran")
		}
	}
	if prompt := ex.prompts[0]; !strings.Contains(prompt, "Review PR 7 (Sửa thanh toán)") || !strings.Contains(prompt, "+func Charge() {}") {
		t.Fatalf("prompt = %s", prompt)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sent) != 1 || !strings.HasPrefix(n.sent[0], "chn_1|c9|") || !strings.Contains(n.sent[0], "LGTM") || !strings.Contains(n.sent[0], "Review PR") {
		t.Fatalf("sent = %q", n.sent)
	}
	if n.convs[0] != "cnv_x" { // a reply to the notice goes on in the run's chat
		t.Fatalf("conversation = %q", n.convs[0])
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d (a closed PR made one?)", len(jobs))
	}
}

// replyExec answers every chat with reply and keeps the prompts.
type replyExec struct {
	mu      sync.Mutex
	prompts []string
	reply   string
}

func (x *replyExec) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	x.mu.Lock()
	x.prompts = append(x.prompts, prompt)
	x.mu.Unlock()
	return "cnv_x", x.reply, nil
}
