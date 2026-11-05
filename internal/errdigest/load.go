package errdigest

import (
	"context"

	"github.com/tung1998/agent-office/internal/storage"
)

// Load groups the failures f matches: the failed jobs, and the failed model
// calls of the same projects and agent (a workflow role or a retried call can
// fail while its job ends well). f.Since bounds both. A model call of a
// project hidden by f.HideOthersIn counts only when its job is f.Me's (ADR-046).
// A connection test (provider_test) is not work that failed: left out.
func Load(ctx context.Context, st storage.Store, f storage.JobFilter) ([]Group, error) {
	jobs, err := st.Jobs().Failed(ctx, f)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(jobs))
	for _, j := range jobs {
		at := j.CreatedAt
		if j.FinishedAt != nil {
			at = *j.FinishedAt
		}
		msg := j.Error
		if msg == "" {
			msg = j.ErrorCode
		}
		events = append(events, Event{Error: msg, Code: j.ErrorCode, JobID: j.ID, ProjectID: j.ProjectID, AgentID: j.AgentID, At: at})
	}
	// model calls belong to no kind, origin, task or chat filter: only the
	// project, agent and time ones apply to them
	if f.Kind == "" && f.Origin == "" && f.OriginID == "" && len(f.OriginIDs) == 0 && f.TaskID == "" && f.ConversationID == "" && f.Source == "" && f.Query == "" {
		runs, err := st.Runs().Errors(ctx, storage.RunFilter{ProjectID: f.ProjectID, ProjectIDs: f.ProjectIDs,
			ExcludeProjectID: f.ExcludeProjectID, AgentID: f.AgentID, Since: f.Since, Limit: f.Limit})
		if err != nil {
			return nil, err
		}
		owner := map[string]string{} // job id → its creator, for hidden projects
		for _, r := range runs {
			if r.Kind == "provider_test" {
				continue
			}
			if f.HideOthersIn != "" && r.ProjectID == f.HideOthersIn {
				if r.JobID == "" {
					continue
				}
				by, ok := owner[r.JobID]
				if !ok {
					if j, err := st.Jobs().Get(ctx, r.JobID); err == nil {
						by = j.CreatedBy
					}
					owner[r.JobID] = by
				}
				if by != f.Me {
					continue
				}
			}
			events = append(events, Event{Error: r.Error, JobID: r.JobID, ProjectID: r.ProjectID, AgentID: r.AgentID,
				Provider: r.ProviderName, Call: true, At: r.CreatedAt})
		}
	}
	return Gather(events), nil
}
