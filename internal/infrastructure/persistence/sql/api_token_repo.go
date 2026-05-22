package sql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// APITokenRepo implements repositories.APITokenRepository using GORM.
type APITokenRepo struct {
	db *gorm.DB
}

func NewAPITokenRepo(db *gorm.DB) *APITokenRepo {
	return &APITokenRepo{db: db}
}

func isAPITokenDuplicate(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "duplicate key value")
}

func (r *APITokenRepo) Create(ctx context.Context, token *entities.APIToken) error {
	model := APITokenModelFromEntity(token)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if isAPITokenDuplicate(err) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create API token: %w", err)
	}
	return nil
}

func (r *APITokenRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.APIToken, error) {
	var model APITokenModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API token: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *APITokenRepo) GetByHash(ctx context.Context, hash string) (*entities.APIToken, error) {
	var model APITokenModel
	err := r.db.WithContext(ctx).First(&model, "token_hash = ?", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API token by hash: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *APITokenRepo) List(ctx context.Context, filter repositories.APITokenFilter) ([]*entities.APIToken, error) {
	var models []APITokenModel
	query := r.db.WithContext(ctx)
	if filter.CreatedByUserID != nil {
		query = query.Where("created_by_user_id = ?", filter.CreatedByUserID.String())
	}
	if filter.OperatorID != nil {
		query = query.Where("operator_id = ?", filter.OperatorID.String())
	}
	if filter.AccountID != nil {
		query = query.Where("account_id = ?", filter.AccountID.String())
	}
	if !filter.IncludeRevoked {
		query = query.Where("revoked_at IS NULL")
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}
	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list API tokens: %w", err)
	}
	out := make([]*entities.APIToken, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

// ListPage returns one keyset-paginated page of API tokens. Self-scope is the
// load-bearing rule here: only admin/system see all tokens; every other role
// sees only tokens they created (CreatedByUserID matches CallerUserID).
//
// Order: (created_at DESC, id DESC).
func (r *APITokenRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.APITokenListFilter) ([]*entities.APIToken, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx)

	// Self-scope: anyone who isn't admin/system sees only their own tokens.
	if !scope.IsAdmin() {
		if scope.CallerUserID == uuid.Nil {
			return nil, "", nil
		}
		query = query.Where("created_by_user_id = ?", scope.CallerUserID.String())
	}

	if !filter.IncludeRevoked {
		query = query.Where("revoked_at IS NULL")
	}
	if filter.Expired != nil {
		now := clock.Now()
		if *filter.Expired {
			query = query.Where("expires_at IS NOT NULL AND expires_at < ?", now)
		} else {
			query = query.Where("expires_at IS NULL OR expires_at >= ?", now)
		}
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

	var models []APITokenModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list api tokens page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	out := make([]*entities.APIToken, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nextCursor, nil
}

func (r *APITokenRepo) UpdateLastUsedAt(ctx context.Context, id uuid.UUID, ts time.Time) error {
	result := r.db.WithContext(ctx).Model(&APITokenModel{}).
		Where("id = ?", id.String()).
		Update("last_used_at", ts)
	if result.Error != nil {
		return fmt.Errorf("failed to update last_used_at: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *APITokenRepo) Revoke(ctx context.Context, id uuid.UUID, ts time.Time) error {
	result := r.db.WithContext(ctx).Model(&APITokenModel{}).
		Where("id = ? AND revoked_at IS NULL", id.String()).
		Updates(map[string]any{
			"revoked_at": ts,
			"updated_at": ts,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to revoke API token: %w", result.Error)
	}
	// RowsAffected == 0 here means either the token does not exist OR it was already
	// revoked. Differentiate with a follow-up read so we can return ErrNotFound only
	// for genuinely missing rows. Revoke-of-revoked is treated as success (idempotent).
	if result.RowsAffected == 0 {
		var m APITokenModel
		if err := r.db.WithContext(ctx).First(&m, "id = ?", id.String()).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return repositories.ErrNotFound
			}
			return fmt.Errorf("failed to read API token after revoke: %w", err)
		}
	}
	return nil
}

func (r *APITokenRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&APITokenModel{}, "id = ?", id.String())
	if result.Error != nil {
		return fmt.Errorf("failed to delete API token: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}
