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

// OperatorRepo implements repositories.OperatorRepository using GORM
type OperatorRepo struct {
	db *gorm.DB
}

// NewOperatorRepo creates a new operator repository
func NewOperatorRepo(db *gorm.DB) *OperatorRepo {
	return &OperatorRepo{db: db}
}

// Create creates a new operator
func (r *OperatorRepo) Create(ctx context.Context, operator *entities.Operator) error {
	model := OperatorModelFromEntity(operator)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create operator: %w", err)
	}

	return nil
}

// GetByID retrieves an operator by ID
func (r *OperatorRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Operator, error) {
	var model OperatorModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves an operator by name
func (r *OperatorRepo) GetByName(ctx context.Context, name string) (*entities.Operator, error) {
	var model OperatorModel

	err := r.db.WithContext(ctx).First(&model, "name = ?", name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get operator by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves an operator by its NATS public key
func (r *OperatorRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error) {
	var model OperatorModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get operator by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves operators with pagination
func (r *OperatorRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Operator, error) {
	var models []OperatorModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list operators: %w", err)
	}

	operators := make([]*entities.Operator, len(models))
	for i, model := range models {
		operators[i] = model.ToEntity()
	}

	return operators, nil
}

// ListPage returns one keyset-paginated page of operators visible under scope.
// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
func (r *OperatorRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.OperatorListFilter) ([]*entities.Operator, string, error) {
	// Zero scope: no access.
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx)

	// Apply scope → SQL narrowing.
	switch {
	case scope.IsAdmin():
		// No narrowing — admin sees all.
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return nil, "", nil
		}
		query = query.Where("id = ?", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		// Account-admin can see the operator that owns their account.
		if scope.ScopeAccountID == nil {
			return nil, "", nil
		}
		var acc AccountModel
		if err := r.db.WithContext(ctx).Select("operator_id").First(&acc, "id = ?", scope.ScopeAccountID.String()).Error; err != nil {
			// Account not found: return empty.
			return nil, "", nil
		}
		query = query.Where("id = ?", acc.OperatorID)
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

	// Cursor predicate: strictly-after (created_at DESC, id DESC).
	if filter.Cursor != "" {
		cursorTime, cursorID, err := DecodeCursor(filter.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", repositories.ErrInvalidCursor, err)
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)",
			cursorTime.UTC(), cursorTime.UTC(), cursorID.String())
	}

	var models []OperatorModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list operators page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	operators := make([]*entities.Operator, len(models))
	for i, m := range models {
		operators[i] = m.ToEntity()
	}
	return operators, nextCursor, nil
}

// Update updates an existing operator
func (r *OperatorRepo) Update(ctx context.Context, operator *entities.Operator) error {
	model := OperatorModelFromEntity(operator)

	result := r.db.WithContext(ctx).Model(&OperatorModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update operator: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Search returns operators visible under scope whose name, description, or
// public_key contain `q` (case-insensitive). Bounded by `limit`. Uses
// LOWER(col) LIKE LOWER(?) so behavior is identical on SQLite and Postgres
// (Postgres LIKE is strictly case-sensitive; ILIKE would work too but the
// LOWER form keeps the repo dialect-agnostic at the cost of a sequential
// scan — acceptable at current scale). Scope narrowing matches ListPage so
// search and list cannot drift apart on the same query shape.
func (r *OperatorRepo) Search(ctx context.Context, scope authz.Scope, q string, limit int) ([]*entities.Operator, error) {
	if scope.IsZero() || limit <= 0 {
		return []*entities.Operator{}, nil
	}
	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return []*entities.Operator{}, nil
		}
		query = query.Where("id = ?", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return []*entities.Operator{}, nil
		}
		var acc AccountModel
		if err := r.db.WithContext(ctx).Select("operator_id").First(&acc, "id = ?", scope.ScopeAccountID.String()).Error; err != nil {
			return []*entities.Operator{}, nil
		}
		query = query.Where("id = ?", acc.OperatorID)
	}

	pat := "%" + escapeLikeParam(q) + "%"
	var models []OperatorModel
	err := query.
		Where(`LOWER(name) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?) OR LOWER(public_key) LIKE LOWER(?)`, pat, pat, pat).
		Order("name ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search operators: %w", err)
	}
	out := make([]*entities.Operator, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

// Delete deletes an operator by ID
func (r *OperatorRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&OperatorModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete operator: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
