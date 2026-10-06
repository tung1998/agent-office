package perm

// FullAccessInput is everything needed to decide full (administrator) access
// for one run (ADR-074, security fix): computed once, from who/what started
// the run — never re-derived from the agent's configuration alone.
type FullAccessInput struct {
	Level      string // the run's effective level, already resolved
	AnswerOnly bool   // the office assistant answers only, proposes nothing
	// ActorTrusted: whoever started this run may use full access — a chat: an
	// admin; a channel message: MayDecide; an automation: triggered by its
	// schedule or run by hand, never a webhook, a PR webhook, or a channel message.
	ActorTrusted bool
	AgentFull    bool
	AgentFullBy  string
	// Override replaces the agent's own full access (an automation's
	// PermissionMode == "override"); it is never added to it.
	Override       bool
	OverrideFull   bool
	OverrideFullBy string
	// IsAdminEmail: that email is still an admin of the office right now (a
	// FullAccess/override an admin turned on only holds while they still are one).
	IsAdminEmail func(email string) bool
}

// EffectiveFullAccess decides whether a run gets full (administrator) access,
// and who enabled it. This is the one place that decision is made (ADR-074
// security fix): a webhook, a PR webhook, or a channel message that is not
// trusted never gets it, however the agent or automation is configured.
func EffectiveFullAccess(in FullAccessInput) (full bool, by string) {
	if !AtLeast(in.Level, Operate) || in.AnswerOnly || !in.ActorTrusted {
		return false, ""
	}
	if in.Override {
		if in.OverrideFull && in.IsAdminEmail(in.OverrideFullBy) {
			return true, in.OverrideFullBy
		}
		return false, "" // overriding but not to full access: never falls back to the agent's own
	}
	if in.AgentFull && in.IsAdminEmail(in.AgentFullBy) {
		return true, in.AgentFullBy
	}
	return false, ""
}
