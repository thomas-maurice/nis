// Package authctx provides context helpers for propagating authenticated callers.
// It lives in infrastructure (not interfaces/grpc/middleware) so that packages
// like internal/application/events can read the actor from context without
// creating an import cycle with the grpc middleware.
//
// Two pieces of state ride in the context:
//
//   - The APIUser is what PermissionService.Can* methods consume. For token-authed
//     requests, the middleware synthesizes an APIUser from the token's role+scope.
//
//   - The Actor is what events.EmitTx records in the audit log. For user-authed
//     requests, the Actor mirrors the APIUser. For token-authed requests, Actor.Type
//     is ActorTypeAPIToken and Actor.ID is the token UUID — this keeps the audit log
//     truthful about which token was used, regardless of any synthetic APIUser.
//
// Both pieces are optional: background tasks set neither, and the events package
// records ActorTypeSystem in that case.
package authctx

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// ContextKey is the type for context keys to avoid collisions.
type ContextKey string

const (
	// UserContextKey is the context key under which an authenticated APIUser is stored.
	UserContextKey ContextKey = "user"

	// ActorContextKey is the context key under which the audit Actor is stored.
	// Set in addition to (not in place of) the APIUser for token-authed requests.
	ActorContextKey ContextKey = "actor"
)

// Actor identifies the caller for audit purposes. Distinct from APIUser: a single
// APIUser may have many long-lived tokens, and audit must attribute each token's
// activity to that token specifically.
type Actor struct {
	Type entities.ActorType
	ID   *uuid.UUID
}

// SetUser stores an authenticated API user in ctx. Called by the auth middleware.
func SetUser(ctx context.Context, user *entities.APIUser) context.Context {
	return context.WithValue(ctx, UserContextKey, user)
}

// GetUser retrieves the authenticated user from ctx.
// Returns (nil, false) when no user is present (e.g. background tasks).
func GetUser(ctx context.Context) (*entities.APIUser, bool) {
	user, ok := ctx.Value(UserContextKey).(*entities.APIUser)
	return user, ok
}

// SetActor stores the audit Actor in ctx. Use this when the audit attribution
// must differ from the synthetic APIUser — most notably for token-authed requests
// where the events should record actor_type='api_token' with the token's ID.
//
// For ordinary user-authed requests there is no need to call SetActor: the events
// package will derive Actor from the APIUser automatically.
func SetActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, ActorContextKey, actor)
}

// GetActor retrieves an explicitly-set audit Actor from ctx.
// Returns (zero, false) when SetActor was not called.
func GetActor(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(ActorContextKey).(Actor)
	return a, ok
}
