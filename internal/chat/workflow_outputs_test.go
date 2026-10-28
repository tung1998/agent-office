package chat

import (
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// A "summary" output that is the run's result is not posted a second time
// under "Đầu ra"; other outputs still are.
func TestOutputsTextSkipsWhatTheResultHolds(t *testing.T) {
	result := "# Bản tin\n\n## 1. Tin mới\n- a\n"
	got := outputsText(storage.WorkflowRun{Result: result, Outputs: map[string]string{"summary": result}})
	if got != "" {
		t.Fatalf("summary equal to the result repeated: %q", got)
	}
	got = outputsText(storage.WorkflowRun{Result: result, Outputs: map[string]string{"summary": result, "verdict": "ok to ship"}})
	if want := "\n\n**Đầu ra**\n- `verdict`: ok to ship"; got != want {
		t.Fatalf("outputsText = %q, want %q", got, want)
	}
}
