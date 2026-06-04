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

// APIUserRepo implements repositories.APIUserRepository using GORM
type APIUserRepo struct {
	db *gorm.DB
}

// NewAPIUserRepo creates a new API user repository
func NewAPIUserRepo(db *gorm.DB) *APIUserRepo {
	return &APIUserRepo{db: db}
}

// Create creates a new API user
func (r *APIUserRepo) Create(ctx context.Context, user *entities.APIUser) error {
	model := APIUserModelFromEntity(user)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create API user: %w", err)
	}

	return nil
}

// GetByID retrieves an API user by ID
func (r *APIUserRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.APIUser, error) {
	var model APIUserModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API user: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByUsername retrieves an API user by username
func (r *APIUserRepo) GetByUsername(ctx context.Context, username string) (*entities.APIUser, error) {
	var model APIUserModel

	err := r.db.WithContext(ctx).First(&model, "username = ?", username).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API user by username: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all API users with pagination
func (r *APIUserRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.APIUser, error) {
	var models []APIUserModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list API users: %w", err)
	}

	users := make([]*entities.APIUser, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// ListPage returns one keyset-paginated page of API users visible under scope.
// Admin sees all; org-admin sees only users in their own organization.
// Other roles return empty — api_user management is admin/org-admin only.
//
// Order: (created_at DESC, id DESC).
func (r *APIUserRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.APIUserListFilter) ([]*entities.APIUser, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOrgAdmin():
		if scope.ScopeOrganizationID == nil {
			return nil, "", nil
		}
		query = query.Where("organization_id = ?", scope.ScopeOrganizationID.String())
	default:
		// operator-admin, account-admin: no api_user listing access.
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)

	if filter.Role != "" {
		query = query.Where("role = ?", filter.Role)
	}
	if filter.AuthSource != "" {
		query = query.Where("auth_source = ?", filter.AuthSource)
	}
	if filter.UsernameLike != "" {
		pattern := "%" + escapeLikeParam(strings.TrimSpace(filter.UsernameLike)) + "%"
		query = query.Where("LOWER(username) LIKE LOWER(?) ESCAPE '\\'", pattern)
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

	var models []APIUserModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list api users page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	out := make([]*entities.APIUser, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nextCursor, nil
}

// GetByExternalSubject retrieves an OIDC api_user by (organization_id, external_subject).
// Returns repositories.ErrNotFound if no matching row exists.
func (r *APIUserRepo) GetByExternalSubject(ctx context.Context, orgID uuid.UUID, subject string) (*entities.APIUser, error) {
	var model APIUserModel
	err := r.db.WithContext(ctx).First(&model,
		"organization_id = ? AND external_subject = ? AND auth_source = 'oidc'",
		orgID.String(), subject,
	).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API user by external subject: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *APIUserRepo) Update(ctx context.Context, user *entities.APIUser) error {
	model := APIUserModelFromEntity(user)

	result := r.db.WithContext(ctx).Model(&APIUserModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update API user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes an API user by ID
func (r *APIUserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&APIUserModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete API user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
