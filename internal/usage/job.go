package usage

import "context"

type jobKey struct{}

// WithJob marks ctx as running for a job: model calls made with it count
// toward that job (its cost, tokens and duration, ADR-040).
func WithJob(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, jobKey{}, jobID)
}

// JobFrom is the job ctx runs for ("" = none).
func JobFrom(ctx context.Context) string {
	id, _ := ctx.Value(jobKey{}).(string)
	return id
}
