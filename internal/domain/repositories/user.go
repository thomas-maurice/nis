package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// UserListFilter holds the filtering and pagination parameters for UserRepository.ListPage.
type UserListFilter struct {
	// Limit is the maximum number of rows to return (0 → default 50; clamped to max 200).
	Limit int
	// Cursor is the opaque pagination cursor ("" → first page).
	Cursor string
	// NameLike is a case-insensitive substring match on name ("" disables).
	NameLike string
	// CreatedSince is an inclusive lower bound on created_at (nil disables).
	CreatedSince *time.Time
	// CreatedUntil is an exclusive upper bound on created_at (nil disables).
	CreatedUntil *time.Time
	// AccountID, if set, further filters to users belonging to this account.
	AccountID *uuid.UUID
	// ScopedSigningKeyID, if set, further filters to users signed by this key.
	ScopedSigningKeyID *uuid.UUID
	// Revoked, if non-nil, filters by revocation status (true = revoked, false = active).
	Revoked *bool
	// ExpiresBefore, if non-nil, filters to users whose JWT expires before this time.
	ExpiresBefore *time.Time
}

// ExpirySweepKind selects which subset of users the JWT expiry sweeper wants
// to process on a given pass. Each kind is matched at the SQL level so we
// don't pull rows the sweeper would have to skip.
type ExpirySweepKind int

const (
	// ExpirySweepKindExpiringSoon — JWTs whose exp is in the future but inside
	// the warn window. Used to fire user.cred.expiring_soon and to feed
	// auto-renew. Filtered by jwt_expires_at < cutoff AND jwt_expires_at >= now.
	ExpirySweepKindExpiringSoon ExpirySweepKind = iota

	// ExpirySweepKindExpired — JWTs whose exp has already elapsed. Used to fire
	// user.cred.expired. Auto-renew is intentionally skipped: silently re-
	// signing a credential NATS already considers dead defeats the purpose of
	// expiry, so the operator must consciously call RegenerateUserJWT.
	ExpirySweepKindExpired
)

// UserRepository defines the interface for user persistence
type UserRepository interface {
	// Create creates a new user
	Create(ctx context.Context, user *entities.User) error

	// GetByID retrieves a user by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)

	// GetByName retrieves a user by name within an account
	GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.User, error)

	// GetByPublicKey retrieves a user by its NATS public key
	GetByPublicKey(ctx context.Context, publicKey string) (*entities.User, error)

	// List retrieves all users with pagination (legacy — kept for internal sweepers).
	List(ctx context.Context, opts ListOptions) ([]*entities.User, error)

	// ListByAccount retrieves users for a specific account (legacy — kept for internal sweepers).
	ListByAccount(ctx context.Context, accountID uuid.UUID, opts ListOptions) ([]*entities.User, error)

	// ListByScopedSigningKey retrieves users signed by a specific scoped signing key (legacy).
	ListByScopedSigningKey(ctx context.Context, scopedKeyID uuid.UUID, opts ListOptions) ([]*entities.User, error)

	// ListPage returns one keyset-paginated page of users visible under scope.
	// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
	ListPage(ctx context.Context, scope authz.Scope, filter UserListFilter) ([]*entities.User, string, error)

	// ListForExpirySweep returns users that match the given sweep kind, scanned
	// at most `limit` rows per call. Revoked users (revoked_at IS NOT NULL) are
	// always excluded — they're being kept dead on purpose.
	//
	// Dedup is enforced at the SQL level via last_expiring_warn_iat / last_expired_alert_iat:
	// rows are only returned when no warn has yet been emitted for the current
	// jwt_issued_at. This survives sweeper restarts and clock skew (the dedup
	// key is the JWT iat, not wallclock time).
	ListForExpirySweep(ctx context.Context, kind ExpirySweepKind, now time.Time, warnWindow time.Duration, limit int) ([]*entities.User, error)

	// Update updates an existing user
	Update(ctx context.Context, user *entities.User) error

	// Delete deletes a user by ID
	Delete(ctx context.Context, id uuid.UUID) error

	// Search returns users whose name, description, or public_key contain the
	// (case-insensitive) substring `q`. Up to `limit` rows. Used by P11 global
	// search. Revoked users are included — the caller can decide whether to
	// hide them.
	Search(ctx context.Context, q string, limit int) ([]*entities.User, error)
}
