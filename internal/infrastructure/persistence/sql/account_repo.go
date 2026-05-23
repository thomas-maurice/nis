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

// AccountRepo implements repositories.AccountRepository using GORM
type AccountRepo struct {
	db *gorm.DB
}

// NewAccountRepo creates a new account repository
func NewAccountRepo(db *gorm.DB) *AccountRepo {
	return &AccountRepo{db: db}
}

// Create creates a new account
func (r *AccountRepo) Create(ctx context.Context, account *entities.Account) error {
	model := AccountModelFromEntity(account)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create account: %w", err)
	}

	return nil
}

// GetByID retrieves an account by ID
func (r *AccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Account, error) {
	var model AccountModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves an account by name within an operator
func (r *AccountRepo) GetByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Account, error) {
	var model AccountModel

	err := r.db.WithContext(ctx).First(&model, "operator_id = ? AND name = ?", operatorID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get account by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves an account by its NATS public key
func (r *AccountRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.Account, error) {
	var model AccountModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get account by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all accounts with pagination
func (r *AccountRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Account, error) {
	var models []AccountModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	accounts := make([]*entities.Account, len(models))
	for i, model := range models {
		accounts[i] = model.ToEntity()
	}

	return accounts, nil
}

// ListByOperator retrieves accounts for a specific operator
func (r *AccountRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Account, error) {
	var models []AccountModel

	query := r.db.WithContext(ctx).Where("operator_id = ?", operatorID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list accounts by operator: %w", err)
	}

	accounts := make([]*entities.Account, len(models))
	for i, model := range models {
		accounts[i] = model.ToEntity()
	}

	return accounts, nil
}

// ListPage returns one keyset-paginated page of accounts visible under scope.
// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
func (r *AccountRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.AccountListFilter) ([]*entities.Account, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx)

	// Scope → SQL narrowing.
	switch {
	case scope.IsAdmin():
		// No narrowing — admin sees all. Apply filter.OperatorID below if set.
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return nil, "", nil
		}
		// Intersection with filter.OperatorID if set and different from scope.
		if filter.OperatorID != nil && *filter.OperatorID != *scope.ScopeOperatorID {
			return nil, "", nil
		}
		query = query.Where("operator_id = ?", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return nil, "", nil
		}
		query = query.Where("id = ?", scope.ScopeAccountID.String())
	}

	// Apply filter.OperatorID for admin scope if set.
	if scope.IsAdmin() && filter.OperatorID != nil {
		query = query.Where("operator_id = ?", filter.OperatorID.String())
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

	var models []AccountModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list accounts page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	accounts := make([]*entities.Account, len(models))
	for i, m := range models {
		accounts[i] = m.ToEntity()
	}
	return accounts, nextCursor, nil
}

// Search returns accounts visible under scope whose name, description, or
// public_key contain `q` (case-insensitive). Bounded by `limit`. Scope
// narrowing matches ListPage. See OperatorRepo.Search for the dialect-uniform
// LOWER(LIKE) rationale.
func (r *AccountRepo) Search(ctx context.Context, scope authz.Scope, q string, limit int) ([]*entities.Account, error) {
	if scope.IsZero() || limit <= 0 {
		return []*entities.Account{}, nil
	}
	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return []*entities.Account{}, nil
		}
		query = query.Where("operator_id = ?", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return []*entities.Account{}, nil
		}
		query = query.Where("id = ?", scope.ScopeAccountID.String())
	}

	pat := "%" + escapeLikeParam(q) + "%"
	var models []AccountModel
	err := query.
		Where(`LOWER(name) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?) OR LOWER(public_key) LIKE LOWER(?)`, pat, pat, pat).
		Order("name ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search accounts: %w", err)
	}
	out := make([]*entities.Account, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

// Update updates an existing account
func (r *AccountRepo) Update(ctx context.Context, account *entities.Account) error {
	model := AccountModelFromEntity(account)

	result := r.db.WithContext(ctx).Model(&AccountModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update account: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes an account by ID
func (r *AccountRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&AccountModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete account: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
