package chat

import (
	"context"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

type modelTierKey struct{}

// WithModelTier makes the runs of ctx use this model tier (strong | balanced |
// fast) instead of the agent's: an automation's cheaper choice.
func WithModelTier(ctx context.Context, tier string) context.Context {
	if tier == "" {
		return ctx
	}
	return context.WithValue(ctx, modelTierKey{}, tier)
}

// ModelTierFrom is the tier ctx asks for ("" = the agent's).
func ModelTierFrom(ctx context.Context) string {
	s, _ := ctx.Value(modelTierKey{}).(string)
	return s
}

// withTier is the agent as it runs under ctx's model tier.
func withTier(ctx context.Context, a storage.Agent) storage.Agent {
	switch tier := ModelTierFrom(ctx); tier {
	case storage.TierStrong, storage.TierBalanced, storage.TierFast:
		a.ModelTier, a.LLMModel = tier, ""
	}
	return a
}

type instructionsKey struct{}

// WithInstructions adds instructions to the system prompt of the runs of ctx:
// an automation's own, set by its admin (a bot's command, ADR-049). They sit
// where no one writing to the bot can reach, unlike a message.
func WithInstructions(ctx context.Context, s string) context.Context {
	if s == "" {
		return ctx
	}
	return context.WithValue(ctx, instructionsKey{}, s)
}

func instructionsOf(ctx context.Context) string {
	s, _ := ctx.Value(instructionsKey{}).(string)
	return s
}

type skillKey struct{}

// WithSkill lets a bot's conversation expand this one skill (a command made
// from it); a bot's conversation expands no other "/name" (review I1).
func WithSkill(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, skillKey{}, name)
}

func skillOf(ctx context.Context) string { s, _ := ctx.Value(skillKey{}).(string); return s }

type noToolsKey struct{}

// WithNoTools makes the runs of ctx tool-less (untrusted text drives them,
// e.g. a channel's scope filter).
func WithNoTools(ctx context.Context) context.Context {
	return context.WithValue(ctx, noToolsKey{}, true)
}

func noTools(ctx context.Context) bool { v, _ := ctx.Value(noToolsKey{}).(bool); return v }

type turnTimeoutKey struct{}

// WithTurnTimeout is how long a turn may take (0: no limit) instead of the
// chat's 20 minutes: an automation's own (ADR-082); its hand-offs keep it.
func WithTurnTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, turnTimeoutKey{}, d)
}

// turnTimeout is the turn's limit (0: none).
func turnTimeout(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(turnTimeoutKey{}).(time.Duration); ok {
		return d
	}
	return defaultTurnTimeout
}

// defaultTurnTimeout: a chat's turn, when nothing says otherwise.
const defaultTurnTimeout = 20 * time.Minute

// withTimeout bounds ctx by d (0: only cancelled).
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

type treeKey struct{}

type treeOpt struct {
	name    string
	noPatch bool
}

// WithTree runs the turn in the worktree name (a Burn item's own) instead of
// the chat's; noPatch: its changes stay there (committed by the caller), no
// diff to approve.
func WithTree(ctx context.Context, name string, noPatch bool) context.Context {
	return context.WithValue(ctx, treeKey{}, treeOpt{name, noPatch})
}

func treeOf(ctx context.Context) string { o, _ := ctx.Value(treeKey{}).(treeOpt); return o.name }
func noPatch(ctx context.Context) bool  { o, _ := ctx.Value(treeKey{}).(treeOpt); return o.noPatch }

type ceilingKey struct{}

// WithCeiling caps a turn's rights at level, whatever the agent's and the
// chat's (a bot's message from someone not in its Admin list, ADR-081).
func WithCeiling(ctx context.Context, level string) context.Context {
	return context.WithValue(ctx, ceilingKey{}, level)
}

func ceilingOf(ctx context.Context) string { v, _ := ctx.Value(ceilingKey{}).(string); return v }

type fullAccessKey struct{}

// WithFullAccess runs the turn with the machine (bypassPermissions, every
// tool): a bot's chat in administrator mode, for someone who may approve there.
func WithFullAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, fullAccessKey{}, true)
}

func fullAccess(ctx context.Context) bool { v, _ := ctx.Value(fullAccessKey{}).(bool); return v }
