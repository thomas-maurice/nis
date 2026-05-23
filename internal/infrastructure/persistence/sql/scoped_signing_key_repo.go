package sql

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// ScopedSigningKeyRepo implements repositories.ScopedSigningKeyRepository using GORM
type ScopedSigningKeyRepo struct {
	db *gorm.DB
}

// NewScopedSigningKeyRepo creates a new scoped signing key repository
func NewScopedSigningKeyRepo(db *gorm.DB) *ScopedSigningKeyRepo {
	return &ScopedSigningKeyRepo{db: db}
}

// Create creates a new scoped signing key
func (r *ScopedSigningKeyRepo) Create(ctx context.Context, key *entities.ScopedSigningKey) error {
	model := ScopedSigningKeyModelFromEntity(key)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	return nil
}

// GetByID retrieves a scoped signing key by ID
func (r *ScopedSigningKeyRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.ScopedSigningKey, error) {
	var model ScopedSigningKeyModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get scoped signing key: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves a scoped signing key by name within an account
func (r *ScopedSigningKeyRepo) GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.ScopedSigningKey, error) {
	var model ScopedSigningKeyModel

	err := r.db.WithContext(ctx).First(&model, "account_id = ? AND name = ?", accountID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get scoped signing key by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves a scoped signing key by its NATS public key
func (r *ScopedSigningKeyRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.ScopedSigningKey, error) {
	var model ScopedSigningKeyModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get scoped signing key by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all scoped signing keys with pagination
func (r *ScopedSigningKeyRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	var models []ScopedSigningKeyModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list scoped signing keys: %w", err)
	}

	keys := make([]*entities.ScopedSigningKey, len(models))
	for i, model := range models {
		keys[i] = model.ToEntity()
	}

	return keys, nil
}

// ListByAccount retrieves scoped signing keys for a specific account
func (r *ScopedSigningKeyRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	var models []ScopedSigningKeyModel

	query := r.db.WithContext(ctx).Where("account_id = ?", accountID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list scoped signing keys by account: %w", err)
	}

	keys := make([]*entities.ScopedSigningKey, len(models))
	for i, model := range models {
		keys[i] = model.ToEntity()
	}

	return keys, nil
}

// ListPage returns one keyset-paginated page of SSKs visible under scope.
// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
//
// Scope mapping:
//   - admin/system: no narrowing.
//   - operator-admin: WHERE account_id IN (SELECT id FROM accounts WHERE operator_id = scope_op).
//   - account-admin: WHERE account_id = scope_acc.
//   - zero scope: no rows.
func (r *ScopedSigningKeyRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.ScopedSigningKeyListFilter) ([]*entities.ScopedSigningKey, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return nil, "", nil
		}
		query = query.Where("account_id IN (SELECT id FROM accounts WHERE operator_id = ?)", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return nil, "", nil
		}
		query = query.Where("account_id = ?", scope.ScopeAccountID.String())
	}

	// Filter intersection: explicit AccountID further narrows. If incompatible
	// with scope (e.g. account-admin asking for a different account), return empty.
	if filter.AccountID != nil {
		if scope.IsAccountAdmin() && scope.ScopeAccountID != nil && *filter.AccountID != *scope.ScopeAccountID {
			return nil, "", nil
		}
		query = query.Where("account_id = ?", filter.AccountID.String())
	}
	if filter.TemplateID != nil {
		query = query.Where("template_id = ?", filter.TemplateID.String())
	}
	if filter.IsPlainSigner != nil {
		query = query.Where("is_plain_signer = ?", *filter.IsPlainSigner)
	}
	if filter.TemplateDrifted != nil {
		query = query.Where("template_drifted = ?", *filter.TemplateDrifted)
	}
	if filter.NameLike != "" {
		pattern := "%" + escapeLikeParam(strings.TrimSpace(filter.NameLike)) + "%"
		query = query.Where("LOWER(name) LIKE LOWER(?) ESCAPE '\\'", pattern)
	}
	if filter.CreatedSince != nil {
		query = query.Where("created_at >= ?", filter.CreatedSince.UTC())
	}
	if filter.CreatedUntil != nil {
		query = query.Where("created_at < ?", filter.CreatedUntil.UTC())
	}

	if filter.Cursor != "" {
		cursorTime, cursorID, err := DecodeCursor(filter.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", repositories.ErrInvalidCursor, err)
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)",
			cursorTime.UTC(), cursorTime.UTC(), cursorID.String())
	}

	var models []ScopedSigningKeyModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list scoped signing keys page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	out := make([]*entities.ScopedSigningKey, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nextCursor, nil
}

// Update updates an existing scoped signing key
func (r *ScopedSigningKeyRepo) Update(ctx context.Context, key *entities.ScopedSigningKey) error {
	model := ScopedSigningKeyModelFromEntity(key)

	result := r.db.WithContext(ctx).Model(&ScopedSigningKeyModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update scoped signing key: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Search returns scoped signing keys visible under scope whose name,
// description, public_key, or any of pub_allow / pub_deny / sub_allow /
// sub_deny contain `q` (case-insensitive). Bounded by `limit`. The pub/sub
// lists are stored as `serializer:json` TEXT columns (see models.go) —
// searching them is a raw LIKE on the JSON-encoded array, which matches
// typical NATS subjects (`metrics.>`, `events.*`) with acceptable
// false-positive behavior (e.g. querying just `>` would match every wildcard
// subject — operators searching for that probably want every wildcard
// anyway). Scope narrowing matches ListPage. See OperatorRepo.Search for
// the dialect-uniform LOWER(LIKE) rationale.
func (r *ScopedSigningKeyRepo) Search(ctx context.Context, scope authz.Scope, q string, limit int) ([]*entities.ScopedSigningKey, error) {
	if scope.IsZero() || limit <= 0 {
		return []*entities.ScopedSigningKey{}, nil
	}
	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return []*entities.ScopedSigningKey{}, nil
		}
		query = query.Where("account_id IN (SELECT id FROM accounts WHERE operator_id = ?)", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return []*entities.ScopedSigningKey{}, nil
		}
		query = query.Where("account_id = ?", scope.ScopeAccountID.String())
	}

	pat := "%" + escapeLikeParam(q) + "%"
	// The four permission columns are JSON-encoded via `serializer:json`, and
	// Go's encoding/json default escapes `<`, `>`, and `&` to their \u00xx
	// literal sequences — so `["metrics.>"]` is stored as the bytes
	// `["metrics.>"]`. To match the stored form, the LIKE pattern needs a
	// literal single backslash; running it through escapeLikeParam would double
	// the backslash (intended for an ESCAPE clause that we don't set), so we
	// build the JSON-form pattern without that doubling. The trade-off: user
	// input containing `%`/`_` overmatches in the four JSON columns only; the
	// user-text columns (name/description/public_key) still get full escaping.
	// Acceptable for v1 — false positives, not security risk.
	patJSON := "%" + jsonHTMLEscape(q) + "%"
	var models []ScopedSigningKeyModel
	err := query.
		Where(`LOWER(name) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?) OR LOWER(public_key) LIKE LOWER(?) OR LOWER(pub_allow) LIKE LOWER(?) OR LOWER(pub_deny) LIKE LOWER(?) OR LOWER(sub_allow) LIKE LOWER(?) OR LOWER(sub_deny) LIKE LOWER(?) OR LOWER(pub_allow) LIKE LOWER(?) OR LOWER(pub_deny) LIKE LOWER(?) OR LOWER(sub_allow) LIKE LOWER(?) OR LOWER(sub_deny) LIKE LOWER(?)`,
			pat, pat, pat, pat, pat, pat, pat, patJSON, patJSON, patJSON, patJSON).
		Order("name ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search scoped signing keys: %w", err)
	}
	out := make([]*entities.ScopedSigningKey, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

// Delete deletes a scoped signing key by ID
func (r *ScopedSigningKeyRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&ScopedSigningKeyModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete scoped signing key: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
