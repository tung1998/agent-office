package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/storage"
)

// fakeDSH answers ACP like `dsh --profile acp`: a session, a tool call, a
// permission request, two messages and the end of the turn.
func fakeDSH(t *testing.T) (bin, log string) {
	tmp := t.TempDir()
	bin, log = filepath.Join(tmp, "dsh"), filepath.Join(tmp, "log")
	fakebin.Write(bin, []byte(`#!/bin/sh
log=`+log+`
echo "ENV $DSH_PERMISSION_MODE $OFFICE_DSH_API_KEY $DSH_TELEMETRY_MODE" >> $log
echo "ARGS $*" >> $log
cat "$4" >> $log
while IFS= read -r line; do
  echo "IN $line" >> $log
  id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
  *'"method":"initialize"'*)
    echo '{"jsonrpc":"2.0","id":'$id',"result":{"protocolVersion":1}}';;
  *'"method":"session/resume"'*'"sessionId":"gone"'*)
    echo '{"jsonrpc":"2.0","id":'$id',"error":{"code":-32602,"message":"Invalid params: unknown session: gone"}}';;
  *'"method":"session/new"'*|*'"method":"session/resume"'*)
    echo '{"jsonrpc":"2.0","id":'$id',"result":{"sessionId":"s1","configOptions":[{"id":"model","currentValue":"[\"office\",\"old\"]"},{"id":"reasoning_effort","currentValue":"high"}]}}';;
  *'"method":"session/set_config_option"'*)
    echo '{"jsonrpc":"2.0","id":'$id',"result":{"configOptions":[]}}';;
  *'"method":"session/prompt"'*)
    echo '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"usage_update","used":5000,"size":262144}}}'
    echo '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"Để xem."}}}}'
    echo '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"read","kind":"other","status":"in_progress","rawInput":{"file_path":"a.go"}}}}'
    echo '{"jsonrpc":"2.0","id":"p1","method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"c1"},"options":[{"optionId":"allow-once","kind":"allow_once"},{"optionId":"reject-once","kind":"reject_once"}]}}'
    IFS= read -r reply
    echo "REPLY $reply" >> $log
    echo '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed"}}}'
    echo '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","messageId":"m2","content":{"type":"text","text":"Xin chào"}}}}'
    echo '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"usage_update","used":5400,"size":262144}}}'
    echo '{"jsonrpc":"2.0","id":'$id',"result":{"stopReason":"end_turn"}}';;
  esac
done
`))
	return bin, log
}

func TestDSHRunner(t *testing.T) {
	bin, log := fakeDSH(t)
	hist := []HistoryItem{{Role: "user", Content: "câu cũ"}, {Role: "assistant", Content: "trả lời cũ"}}
	req := RunRequest{Bin: bin, APIKey: "sk-test", Model: "glm-4.6", Effort: "max", System: "HƯỚNG DẪN", History: hist, Prompt: "mới",
		WorkDir: filepath.Dir(bin), Provider: storage.Provider{Kind: storage.ProviderDSH, Name: "GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4"},
		Office: &OfficeAccess{MCPURL: "http://127.0.0.1:2704/mcp", Token: "tok", Gateway: []string{"jira"}}}
	var text strings.Builder
	res, err := dshRunner{}.Run(context.Background(), req, func(e Event) {
		if e.Type == "text" {
			text.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Để xem.\n\nXin chào" || text.String() != res.Text || res.SessionID != "s1" || res.Usage.Model != "glm-4.6" ||
		res.Context != (ContextUse{Tokens: 5400, Window: 262144}) {
		t.Fatalf("text=%q res=%+v", text.String(), res)
	}
	if len(res.Tools) != 1 || res.Tools[0].Summary != "Đọc a.go" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	raw, _ := os.ReadFile(log)
	got := string(raw)
	for _, want := range []string{
		"ENV read-only sk-test DISABLED", "--profile acp --patch",
		`"apiKeyEnv": "OFFICE_DSH_API_KEY"`, `"api": "openai-completions"`, `"baseURL": "https://open.bigmodel.cn/api/paas/v4"`,
		`"id": "glm-4.6"`, `"id": "session-log-deepseek"`, `"id": "tool-web"`,
		`"method":"session/new"`, `"url":"http://127.0.0.1:2704/mcp"`, `"value":"Bearer tok"`, `"url":"http://127.0.0.1:2704/mcp/s/jira"`,
		`"configId":"model","sessionId":"s1","value":"[\"office\",\"glm-4.6\"]"`, `"configId":"reasoning_effort","sessionId":"s1","value":"max"`,
		"HƯỚNG DẪN", "câu cũ", `"optionId":"reject-once"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sk-test\"") {
		t.Fatalf("the key reached the patch or the protocol:\n%s", got)
	}

	// going on in the session, allowed to edit: only the new message
	os.Remove(log)
	req.SessionID, req.Write = "s1", true
	if _, err := (dshRunner{}).Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(log)
	if got = string(raw); !strings.Contains(got, `"method":"session/resume"`) || !strings.Contains(got, "ENV workspace-write") || strings.Contains(got, "HƯỚNG DẪN") {
		t.Fatalf("resume call:\n%s", got)
	}

	// a session dsh lost: start over with the transcript
	os.Remove(log)
	req.SessionID = "gone"
	if res, err = (dshRunner{}).Run(context.Background(), req, func(Event) {}); err != nil || res.SessionID != "s1" {
		t.Fatalf("lost: %+v %v", res, err)
	}
	raw, _ = os.ReadFile(log)
	if got = string(raw); strings.Count(got, "ARGS") != 2 || !strings.Contains(got, "HƯỚNG DẪN") {
		t.Fatalf("fallback calls:\n%s", got)
	}

	// full access: allowed, no sandbox
	os.Remove(log)
	req.SessionID, req.FullAccess = "", true
	if _, err := (dshRunner{}).Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(log)
	if got = string(raw); !strings.Contains(got, "ENV danger-full-access") || !strings.Contains(got, `"optionId":"allow-once"`) {
		t.Fatalf("full access call:\n%s", got)
	}
}

func TestDSHRunnerNeedsKeyAndModel(t *testing.T) {
	req := RunRequest{Bin: "/nonexistent/dsh", Provider: storage.Provider{Kind: storage.ProviderDSH, BaseURL: "https://openrouter.ai/api/v1"}}
	if _, err := (dshRunner{}).Run(context.Background(), req, func(Event) {}); err != llm.ErrNoAPIKey {
		t.Fatalf("no key: %v", err)
	}
	req.APIKey = "k"
	if _, err := (dshRunner{}).Run(context.Background(), req, func(Event) {}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("no model: %v", err)
	}
	// DeepSeek's own API has a default model
	req.Provider.BaseURL = ""
	if m, err := dshModel(req); err != nil || m != dshDefaultModel {
		t.Fatalf("deepseek default: %q %v", m, err)
	}
}

func TestHarnessAPI(t *testing.T) {
	for in, want := range map[string][2]string{
		"":                               {llm.HarnessOpenAI, llm.DeepSeekAPI},
		"https://openrouter.ai/api/v1/":  {llm.HarnessOpenAI, "https://openrouter.ai/api/v1"},
		"https://api.anthropic.com/v1":   {llm.HarnessAnthropic, "https://api.anthropic.com"},
		"https://api.z.ai/api/anthropic": {llm.HarnessAnthropic, "https://api.z.ai/api/anthropic"},
		"http://localhost:11434/v1":      {llm.HarnessOpenAI, "http://localhost:11434/v1"},
	} {
		if api, base := llm.HarnessAPI(in); api != want[0] || base != want[1] {
			t.Errorf("%q → %s %s", in, api, base)
		}
	}
}

func TestDSHPatchNoTools(t *testing.T) {
	raw, err := dshPatch(RunRequest{NoTools: true, Provider: storage.Provider{Kind: storage.ProviderDSH}}, "m")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range dshNoTools {
		if !strings.Contains(string(raw), `"id": "`+id+`"`) {
			t.Fatalf("%s left on:\n%s", id, raw)
		}
	}
	if dshPermission(RunRequest{NoTools: true, FullAccess: true}) != "read-only" {
		t.Fatal("no tools must stay read-only")
	}
}
