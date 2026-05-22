package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// APITokenFilter narrows ListAPITokens results (legacy). CreatedByUserID is the
// primary scoping axis for non-admin callers (handler enforces it).
type APITokenFilter struct {
	CreatedByUserID *uuid.UUID
	OperatorID      *uuid.UUID
	AccountID       *uuid.UUID
	IncludeRevoked  bool // when false, revoked tokens are excluded
	Limit           int
	Offset          int
}

// APITokenListFilter is the keyset-paginated filter shape. Self-scope (the
// caller seeing only their own tokens) lives in authz.Scope.CallerUserID; the
// repo applies it for non-admin scopes.
type APITokenListFilter struct {
	Limit          int
	Cursor         string
	IncludeRevoked bool
	Expired        *bool // true → expired only; false → not-expired only; nil → either
	CreatedSince   *time.Time
	CreatedUntil   *time.Time
}

// APITokenRepository defines persistence for service-account API tokens.
type APITokenRepository interface {
	Create(ctx context.Context, token *entities.APIToken) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.APIToken, error)
	// GetByHash is the hot-path lookup used by the auth middleware on every
	// token-authed RPC. Index on token_hash makes this O(1).
	GetByHash(ctx context.Context, hash string) (*entities.APIToken, error)
	List(ctx context.Context, filter APITokenFilter) ([]*entities.APIToken, error)
	// ListPage returns one keyset-paginated page of tokens. Self-scope for
	// non-admin callers is enforced at the SQL layer using
	// scope.CallerUserID (admins see all; everyone else sees only their own
	// tokens).
	ListPage(ctx context.Context, scope authz.Scope, filter APITokenListFilter) ([]*entities.APIToken, string, error)
	// UpdateLastUsedAt is a focused write used by the coalescing flusher — it
	// updates only the last_used_at column to avoid colliding with Revoke or other
	// mutations that may run concurrently.
	UpdateLastUsedAt(ctx context.Context, id uuid.UUID, ts time.Time) error
	// Revoke sets revoked_at = ts on the token. Idempotent: revoking an already-
	// revoked token is a no-op (does not return ErrNotFound for the second call).
	Revoke(ctx context.Context, id uuid.UUID, ts time.Time) error
	Delete(ctx context.Context, id uuid.UUID) error
}
