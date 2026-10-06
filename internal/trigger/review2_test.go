package trigger_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

func TestScriptChildEscapingTheGroupDoesNotHang(t *testing.T) { // I1
	start := time.Now()
	_, _, timedOut, err := trigger.RunScript(context.Background(), t.TempDir(),
		storage.AutomationScript{Lang: "python", Body: "import os,time\nif os.fork()==0:\n    os.setsid(); time.sleep(30)\ntime.sleep(30)", TimeoutS: 1}, nil, "")
	if err != nil || !timedOut || time.Since(start) > 7*time.Second {
		t.Fatalf("timeout=%v err=%v after %v", timedOut, err, time.Since(start))
	}
}

func TestScriptLeavingABackgroundJobEndsWhenItExits(t *testing.T) { // I1
	start := time.Now()
	out, code, timedOut, err := trigger.RunScript(context.Background(), t.TempDir(),
		storage.AutomationScript{Lang: "bash", Body: "sleep 600 & echo started", TimeoutS: 30}, nil, "")
	if err != nil || timedOut || code != 0 || time.Since(start) > 7*time.Second || out == "" {
		t.Fatalf("out=%q code=%d timeout=%v err=%v after %v", out, code, timedOut, err, time.Since(start))
	}
}

func TestScriptDoesNotSeeAIKeys(t *testing.T) { // M3
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("KEEP_ME", "ok")
	out, _, _, err := trigger.RunScript(context.Background(), t.TempDir(),
		storage.AutomationScript{Lang: "bash", Body: `echo "[$ANTHROPIC_API_KEY][$OPENAI_API_KEY][$KEEP_ME]"`}, nil, "")
	if err != nil || out != "[][][ok]\n" {
		t.Fatalf("out = %q %v", out, err)
	}
}

func TestEscalationDoesNotResetScriptFailures(t *testing.T) { // I2
	ctx := context.Background()
	st, p := openStore(t)
	r := trigger.New(st, &recExec{})
	a := scriptAutomation(t, st, p, "exit 1", "failure")
	a.Script.TimeoutS = 1
	a.Script.Body = "sleep 5"
	st.Automations().Update(ctx, a)
	runOnce(t, r, st, a, "")
	if a, _ = st.Automations().Get(ctx, a.ID); a.Failures != 1 {
		t.Fatalf("failures after a timeout and a good escalation = %d, want 1", a.Failures)
	}
}
