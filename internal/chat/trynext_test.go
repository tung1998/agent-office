package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// A read-only turn that read files then ended with no answer is asked again
// on the next connection; one that may change things is not repeated.
func TestTryNextReadOnlyEmptyAnswer(t *testing.T) {
	ctx := context.Background()
	err := errors.New("agy kết thúc lượt mà không trả lời: tool lỗi")
	read := RunResult{Tools: []storage.ToolCall{{Name: "view_file"}}, Usage: llm.Result{OutputTokens: 40}}
	if !tryNext(ctx, read, err, true) {
		t.Fatal("a read-only turn with no answer moves on")
	}
	if tryNext(ctx, read, err, false) {
		t.Fatal("a turn that may have changed things is not repeated")
	}
	if tryNext(ctx, RunResult{Text: "half an answer", Tools: read.Tools}, err, true) {
		t.Fatal("a turn that answered something is kept")
	}
	if !tryNext(ctx, RunResult{}, err, false) {
		t.Fatal("nothing done: any turn moves on")
	}
	if !readOnly(perm.Read, RunRequest{}) || readOnly(perm.Propose, RunRequest{}) || readOnly(perm.Read, RunRequest{FullAccess: true}) {
		t.Fatal("readOnly: below propose, no writes, no full access")
	}
}
