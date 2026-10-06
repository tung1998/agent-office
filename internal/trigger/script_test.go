package trigger_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

func TestRunScriptOutputAndExit(t *testing.T) {
	out, code, timedOut, err := trigger.RunScript(context.Background(), t.TempDir(),
		storage.AutomationScript{Lang: "bash", Body: "read x; echo \"got $x $OFFICE_PAYLOAD\"; echo oops >&2; exit 3"},
		[]string{"OFFICE_PAYLOAD=pl"}, "in\n")
	if err != nil || code != 3 || timedOut || !strings.Contains(out, "got in pl") || !strings.Contains(out, "oops") {
		t.Fatalf("out=%q code=%d timeout=%v err=%v", out, code, timedOut, err)
	}
}

func TestRunScriptTimeoutKillsChildren(t *testing.T) {
	start := time.Now()
	_, _, timedOut, err := trigger.RunScript(context.Background(), t.TempDir(),
		storage.AutomationScript{Lang: "bash", Body: "sleep 30 & wait", TimeoutS: 1}, nil, "")
	if err != nil || !timedOut || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout=%v err=%v after %v", timedOut, err, time.Since(start))
	}
}

func TestRunScriptKeepsTheTail(t *testing.T) {
	out, code, _, err := trigger.RunScript(context.Background(), t.TempDir(),
		storage.AutomationScript{Lang: "bash", Body: "head -c 1048576 /dev/zero | tr '\\0' 'a'; echo END"}, nil, "")
	if err != nil || code != 0 || len(out) > 64<<10 || !strings.HasSuffix(strings.TrimSpace(out), "END") {
		t.Fatalf("len=%d code=%d err=%v tail=%q", len(out), code, err, out[max(0, len(out)-20):])
	}
}

func TestSignals(t *testing.T) {
	got := trigger.Signals("a\n@@agent: lỗi DB tăng\nb\n  @@agent:  xem lại cron \n")
	if len(got) != 2 || got[0] != "lỗi DB tăng" || got[1] != "xem lại cron" {
		t.Fatalf("signals = %q", got)
	}
	if _, _, _, err := trigger.RunScript(context.Background(), t.TempDir(), storage.AutomationScript{Lang: "ruby", Body: "x"}, nil, ""); err == nil {
		t.Fatal("unknown language ran")
	}
}
