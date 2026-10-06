// Package actor carries "who is doing this" through a request context, for
// audit, revisions and usage records.
package actor

import (
	"context"
	"strings"
)

type key struct{}

// With returns ctx tagged with actor (e.g. "human:a@b.c", "cli", "system").
func With(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, key{}, actor)
}

// From returns the actor, or "system".
func From(ctx context.Context) string {
	if a, ok := ctx.Value(key{}).(string); ok && a != "" {
		return a
	}
	return "system"
}

// Source is where something an actor started came from: web (a person on
// the dashboard), discord, telegram or auto (an automation).
func Source(actor string) string {
	kind, _, _ := strings.Cut(actor, ":")
	switch kind {
	case "discord", "telegram", "auto":
		return kind
	}
	return "web"
}
