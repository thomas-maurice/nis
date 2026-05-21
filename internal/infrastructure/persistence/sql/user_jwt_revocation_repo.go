package sql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// UserJWTRevocationRepo implements repositories.UserJWTRevocationRepository using GORM.
type UserJWTRevocationRepo struct {
	db *gorm.DB
}

// NewUserJWTRevocationRepo creates a new user JWT revocation repository.
func NewUserJWTRevocationRepo(db *gorm.DB) *UserJWTRevocationRepo {
	return &UserJWTRevocationRepo{db: db}
}

func (r *UserJWTRevocationRepo) Create(ctx context.Context, rev *entities.UserJWTRevocation) error {
	m := UserJWTRevocationModelFromEntity(rev)
	// Normalize to UTC at the persistence boundary. SQLite stores time.Time
	// using the value's own Location, and lexical comparisons in `WHERE
	// jwt_exp <= ?` quietly go wrong when one side is local time and the
	// other is UTC. Stamping UTC on the way in (and again on every
	// comparison; see ListPrunable) means callers don't have to remember.
	m.RevokedAt = m.RevokedAt.UTC()
	m.JWTExp = m.JWTExp.UTC()
	if m.PrunedAt != nil {
		u := m.PrunedAt.UTC()
		m.PrunedAt = &u
	}
	m.CreatedAt = m.CreatedAt.UTC()
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return fmt.Errorf("failed to create user JWT revocation: %w", err)
	}
	return nil
}

func (r *UserJWTRevocationRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserJWTRevocation, error) {
	var m UserJWTRevocationModel
	if err := r.db.WithContext(ctx).First(&m, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user JWT revocation: %w", err)
	}
	return m.ToEntity(), nil
}

func (r *UserJWTRevocationRepo) ListActiveByAccount(ctx context.Context, accountID uuid.UUID) ([]*entities.UserJWTRevocation, error) {
	var models []UserJWTRevocationModel
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND pruned_at IS NULL", accountID.String()).
		Order("revoked_at ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list active user JWT revocations for account: %w", err)
	}
	out := make([]*entities.UserJWTRevocation, len(models))
	for i := range models {
		out[i] = models[i].ToEntity()
	}
	return out, nil
}

func (r *UserJWTRevocationRepo) CountActiveByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&UserJWTRevocationModel{}).
		Where("account_id = ? AND pruned_at IS NULL", accountID.String()).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count active user JWT revocations: %w", err)
	}
	return n, nil
}

func (r *UserJWTRevocationRepo) ListPrunable(ctx context.Context, before time.Time, limit int) ([]*entities.UserJWTRevocation, error) {
	var models []UserJWTRevocationModel
	q := r.db.WithContext(ctx).
		Where("pruned_at IS NULL").
		Where("jwt_exp <= ?", before.UTC()).
		Order("jwt_exp ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list prunable user JWT revocations: %w", err)
	}
	out := make([]*entities.UserJWTRevocation, len(models))
	for i := range models {
		out[i] = models[i].ToEntity()
	}
	return out, nil
}

func (r *UserJWTRevocationRepo) DeletePrunedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	cutoffUTC := cutoff.UTC()
	// LIMIT on DELETE is not portable: SQLite needs SQLITE_ENABLE_UPDATE_DELETE_LIMIT
	// at build time, and Postgres doesn't support it at all. The
	// `WHERE id IN (SELECT id ... LIMIT n)` form works on both.
	if limit > 0 {
		sub := r.db.WithContext(ctx).
			Model(&UserJWTRevocationModel{}).
			Select("id").
			Where("pruned_at IS NOT NULL").
			Where("pruned_at < ?", cutoffUTC).
			Limit(limit)
		result := r.db.WithContext(ctx).
			Where("id IN (?)", sub).
			Delete(&UserJWTRevocationModel{})
		if result.Error != nil {
			return 0, fmt.Errorf("failed to delete pruned user JWT revocations: %w", result.Error)
		}
		return result.RowsAffected, nil
	}
	result := r.db.WithContext(ctx).
		Where("pruned_at IS NOT NULL").
		Where("pruned_at < ?", cutoffUTC).
		Delete(&UserJWTRevocationModel{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete pruned user JWT revocations: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (r *UserJWTRevocationRepo) MarkPruned(ctx context.Context, ids []uuid.UUID, prunedAt time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = id.String()
	}
	err := r.db.WithContext(ctx).
		Model(&UserJWTRevocationModel{}).
		Where("id IN ?", idStrs).
		Update("pruned_at", prunedAt.UTC()).Error
	if err != nil {
		return fmt.Errorf("failed to mark revocations pruned: %w", err)
	}
	return nil
}
