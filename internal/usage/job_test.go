package usage_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

func TestRecordCarriesJob(t *testing.T) {
	ctx := context.Background()
	u := usage.New(newStore(t), time.UTC)
	r, err := u.Record(usage.WithJob(ctx, "job_1"), usage.Meta{Kind: "chat"}, storage.Provider{Name: "P"}, "m", llm.Result{InputTokens: 1}, nil)
	if err != nil || r.JobID != "job_1" {
		t.Fatalf("run = %+v %v", r, err)
	}
	if usage.JobFrom(ctx) != "" {
		t.Fatal("empty ctx has a job")
	}
}
