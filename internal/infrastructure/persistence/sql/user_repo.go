package sql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// UserRepo implements repositories.UserRepository using GORM
type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo creates a new user repository
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Create creates a new user
func (r *UserRepo) Create(ctx context.Context, user *entities.User) error {
	model := UserModelFromEntity(user)
	// Normalize time fields to UTC — see Update for rationale.
	normalizeUTC := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	model.JWTIssuedAt = normalizeUTC(model.JWTIssuedAt)
	model.JWTExpiresAt = normalizeUTC(model.JWTExpiresAt)
	model.RevokedAt = normalizeUTC(model.RevokedAt)
	model.LastExpiringWarnIAT = normalizeUTC(model.LastExpiringWarnIAT)
	model.LastExpiredAlertIAT = normalizeUTC(model.LastExpiredAlertIAT)
	model.CreatedAt = model.CreatedAt.UTC()
	model.UpdatedAt = model.UpdatedAt.UTC()

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// GetByID retrieves a user by ID
func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	var model UserModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves a user by name within an account
func (r *UserRepo) GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.User, error) {
	var model UserModel

	err := r.db.WithContext(ctx).First(&model, "account_id = ? AND name = ?", accountID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves a user by its NATS public key
func (r *UserRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.User, error) {
	var model UserModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all users with pagination
func (r *UserRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.User, error) {
	var models []UserModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	users := make([]*entities.User, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// ListByAccount retrieves users for a specific account
func (r *UserRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	var models []UserModel

	query := r.db.WithContext(ctx).Where("account_id = ?", accountID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users by account: %w", err)
	}

	users := make([]*entities.User, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// ListByScopedSigningKey retrieves users signed by a specific scoped signing key
func (r *UserRepo) ListByScopedSigningKey(ctx context.Context, scopedKeyID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	var models []UserModel

	query := r.db.WithContext(ctx).Where("scoped_signing_key_id = ?", scopedKeyID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users by scoped signing key: %w", err)
	}

	users := make([]*entities.User, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// ListPage returns one keyset-paginated page of users visible under scope.
// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
func (r *UserRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.UserListFilter) ([]*entities.User, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx).Table("users")

	// Scope → SQL narrowing.
	switch {
	case scope.IsAdmin():
		// No narrowing.
	case scope.IsOrgAdmin():
		if scope.ScopeOrganizationID == nil {
			return nil, "", nil
		}
		// Org-admin sees users whose account belongs to any operator in their org.
		query = query.Where("account_id IN (SELECT id FROM accounts WHERE operator_id IN (SELECT id FROM operators WHERE organization_id = ?))", scope.ScopeOrganizationID.String())
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return nil, "", nil
		}
		// Operator-admin sees users whose account belongs to their operator.
		query = query.Where("EXISTS (SELECT 1 FROM accounts WHERE accounts.id = users.account_id AND accounts.operator_id = ?)", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return nil, "", nil
		}
		query = query.Where("account_id = ?", scope.ScopeAccountID.String())
	}

	// Additional filters.
	if filter.AccountID != nil {
		query = query.Where("account_id = ?", filter.AccountID.String())
	}
	if filter.ScopedSigningKeyID != nil {
		query = query.Where("scoped_signing_key_id = ?", filter.ScopedSigningKeyID.String())
	}
	if filter.Revoked != nil {
		if *filter.Revoked {
			query = query.Where("revoked_at IS NOT NULL")
		} else {
			query = query.Where("revoked_at IS NULL")
		}
	}
	if filter.ExpiresBefore != nil {
		query = query.Where("jwt_expires_at < ?", filter.ExpiresBefore.UTC())
	}

	// NameLike filter. escapeLikeParam escapes %, _, and \ so they are treated as
	// literals; the ESCAPE '\' clause activates SQLite's backslash-escape mode.
	if filter.NameLike != "" {
		pattern := "%" + escapeLikeParam(strings.TrimSpace(filter.NameLike)) + "%"
		query = query.Where("LOWER(name) LIKE LOWER(?) ESCAPE '\\'", pattern)
	}

	// Time bound filters.
	if filter.CreatedSince != nil {
		query = query.Where("created_at >= ?", filter.CreatedSince.UTC())
	}
	if filter.CreatedUntil != nil {
		query = query.Where("created_at < ?", filter.CreatedUntil.UTC())
	}

	// Cursor predicate.
	if filter.Cursor != "" {
		cursorTime, cursorID, err := DecodeCursor(filter.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", repositories.ErrInvalidCursor, err)
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)",
			cursorTime.UTC(), cursorTime.UTC(), cursorID.String())
	}

	var models []UserModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list users page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	users := make([]*entities.User, len(models))
	for i, m := range models {
		users[i] = m.ToEntity()
	}
	return users, nextCursor, nil
}

// ListForExpirySweep returns rows matching the sweeper's target subset. See the
// repositories.UserRepository docs for the dedup primitive.
//
// Times are forced to UTC for the WHERE clauses because SQLite stores
// time.Time using the binding's Location; mixing local and UTC bindings
// yields silently-wrong lexical comparisons.
func (r *UserRepo) ListForExpirySweep(ctx context.Context, kind repositories.ExpirySweepKind, now time.Time, warnWindow time.Duration, limit int) ([]*entities.User, error) {
	var models []UserModel
	nowUTC := now.UTC()
	q := r.db.WithContext(ctx).
		Where("jwt_expires_at IS NOT NULL").
		Where("revoked_at IS NULL")

	switch kind {
	case repositories.ExpirySweepKindExpiringSoon:
		// JWT expires within the warn window, still in the future, AND no warn
		// has yet been emitted for the current iat.
		cutoff := nowUTC.Add(warnWindow)
		q = q.Where("jwt_expires_at > ?", nowUTC).
			Where("jwt_expires_at <= ?", cutoff).
			Where("(last_expiring_warn_iat IS NULL OR last_expiring_warn_iat <> jwt_issued_at)")
	case repositories.ExpirySweepKindExpired:
		// JWT exp already past, and no expired-alert has been emitted for the
		// current iat. Auto-renew is intentionally skipped at the service
		// layer for this kind (see ExpirySweepKindExpired godoc).
		q = q.Where("jwt_expires_at <= ?", nowUTC).
			Where("(last_expired_alert_iat IS NULL OR last_expired_alert_iat <> jwt_issued_at)")
	default:
		return nil, fmt.Errorf("unknown expiry sweep kind: %d", kind)
	}

	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Order("jwt_expires_at ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users for expiry sweep: %w", err)
	}
	out := make([]*entities.User, len(models))
	for i := range models {
		out[i] = models[i].ToEntity()
	}
	return out, nil
}

// Update updates an existing user
func (r *UserRepo) Update(ctx context.Context, user *entities.User) error {
	model := UserModelFromEntity(user)
	// Normalize time fields to UTC for consistent lexical comparison in SQLite.
	normalizeUTC := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	model.JWTIssuedAt = normalizeUTC(model.JWTIssuedAt)
	model.JWTExpiresAt = normalizeUTC(model.JWTExpiresAt)
	model.RevokedAt = normalizeUTC(model.RevokedAt)
	model.LastExpiringWarnIAT = normalizeUTC(model.LastExpiringWarnIAT)
	model.LastExpiredAlertIAT = normalizeUTC(model.LastExpiredAlertIAT)
	model.UpdatedAt = model.UpdatedAt.UTC()

	result := r.db.WithContext(ctx).Model(&UserModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Search returns users visible under scope whose name, description, or
// public_key contain `q` (case-insensitive). Bounded by `limit`. Revoked
// users are included; the caller decides whether to hide them. Scope
// narrowing matches ListPage (operator-admin sees users whose parent account
// belongs to their operator; account-admin sees users in their account).
func (r *UserRepo) Search(ctx context.Context, scope authz.Scope, q string, limit int) ([]*entities.User, error) {
	if scope.IsZero() || limit <= 0 {
		return []*entities.User{}, nil
	}
	query := r.db.WithContext(ctx).Table("users")

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOrgAdmin():
		if scope.ScopeOrganizationID == nil {
			return []*entities.User{}, nil
		}
		query = query.Where("account_id IN (SELECT id FROM accounts WHERE operator_id IN (SELECT id FROM operators WHERE organization_id = ?))", scope.ScopeOrganizationID.String())
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return []*entities.User{}, nil
		}
		query = query.Where("EXISTS (SELECT 1 FROM accounts WHERE accounts.id = users.account_id AND accounts.operator_id = ?)", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return []*entities.User{}, nil
		}
		query = query.Where("account_id = ?", scope.ScopeAccountID.String())
	}

	pat := "%" + escapeLikeParam(q) + "%"
	var models []UserModel
	err := query.
		Where(`LOWER(name) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?) OR LOWER(public_key) LIKE LOWER(?)`, pat, pat, pat).
		Order("name ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search users: %w", err)
	}
	out := make([]*entities.User, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

// Delete deletes a user by ID
func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&UserModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
