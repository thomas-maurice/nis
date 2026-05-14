// Package authctx provides context helpers for propagating authenticated users.
// It lives in infrastructure (not interfaces/grpc/middleware) so that packages
// like internal/application/events can read the actor from context without
// creating an import cycle with the grpc middleware.
package authctx

import (
	"context"

	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// ContextKey is the type for user-context keys to avoid collisions.
type ContextKey string

// UserContextKey is the context key under which an authenticated APIUser is stored.
const UserContextKey ContextKey = "user"

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
