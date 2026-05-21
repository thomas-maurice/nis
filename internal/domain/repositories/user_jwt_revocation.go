package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// UserJWTRevocationRepository persists user-JWT revocations (P2). The active
// rows for an account (PrunedAt IS NULL) are read by JWT regeneration paths
// and flattened into the account JWT's NATS Revocations map; the sweeper
// prunes rows whose JWTExp has elapsed.
type UserJWTRevocationRepository interface {
	// Create persists a new revocation row.
	Create(ctx context.Context, rev *entities.UserJWTRevocation) error

	// GetByID returns a single revocation by ID.
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserJWTRevocation, error)

	// ListActiveByAccount returns non-pruned revocations for the account, used by
	// AccountClaims regeneration. Ordered oldest-first so a deterministic JWT
	// is produced for identical state across runs (helps drift comparisons).
	ListActiveByAccount(ctx context.Context, accountID uuid.UUID) ([]*entities.UserJWTRevocation, error)

	// CountActiveByAccount is a cheap helper for surfacing the revocation-count
	// badge in the UI without paying the cost of materialising rows.
	CountActiveByAccount(ctx context.Context, accountID uuid.UUID) (int64, error)

	// ListPrunable returns revocations whose JWT exp has passed and that have not
	// yet been pruned. The sweeper uses this to find candidates to remove from
	// the account JWT's Revocations map.
	ListPrunable(ctx context.Context, before time.Time, limit int) ([]*entities.UserJWTRevocation, error)

	// MarkPruned flags the rows identified by ids as pruned with the given
	// timestamp. Called after the account JWT has been regenerated without them.
	MarkPruned(ctx context.Context, ids []uuid.UUID, prunedAt time.Time) error

	// DeletePrunedBefore hard-deletes pruned revocation rows whose pruned_at
	// is strictly before cutoff. Rows with pruned_at IS NULL are never touched
	// (those still belong in the parent account JWT's Revocations map). When
	// limit > 0 the delete is capped at that many rows per call so one sweep
	// tick stays cheap on large datasets; limit == 0 deletes all eligible
	// rows. Returns the number of rows deleted.
	DeletePrunedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
