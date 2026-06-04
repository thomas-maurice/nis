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

// -----------------------------------------------------------------------
// OrganizationRepo
// -----------------------------------------------------------------------

// OrganizationRepo implements repositories.OrganizationRepository using GORM.
type OrganizationRepo struct {
	db *gorm.DB
}

// NewOrganizationRepo creates a new OrganizationRepo.
func NewOrganizationRepo(db *gorm.DB) *OrganizationRepo {
	return &OrganizationRepo{db: db}
}

func (r *OrganizationRepo) Create(ctx context.Context, org *entities.Organization) error {
	model := OrganizationModelFromEntity(org)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create organization: %w", err)
	}
	return nil
}

func (r *OrganizationRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Organization, error) {
	var model OrganizationModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get organization: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *OrganizationRepo) GetBySlug(ctx context.Context, slug string) (*entities.Organization, error) {
	var model OrganizationModel
	err := r.db.WithContext(ctx).First(&model, "slug = ?", slug).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get organization by slug: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *OrganizationRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Organization, error) {
	var models []OrganizationModel
	q := r.db.WithContext(ctx).Order("created_at DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list organizations: %w", err)
	}
	out := make([]*entities.Organization, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

func (r *OrganizationRepo) Update(ctx context.Context, org *entities.Organization) error {
	model := OrganizationModelFromEntity(org)
	res := r.db.WithContext(ctx).Model(&OrganizationModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)
	if res.Error != nil {
		return fmt.Errorf("failed to update organization: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *OrganizationRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&OrganizationModel{}, "id = ?", id.String())
	if res.Error != nil {
		return fmt.Errorf("failed to delete organization: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------
// OrganizationSSOConfigRepo
// -----------------------------------------------------------------------

// OrganizationSSOConfigRepo implements repositories.OrganizationSSOConfigRepository.
type OrganizationSSOConfigRepo struct {
	db *gorm.DB
}

// NewOrganizationSSOConfigRepo creates a new OrganizationSSOConfigRepo.
func NewOrganizationSSOConfigRepo(db *gorm.DB) *OrganizationSSOConfigRepo {
	return &OrganizationSSOConfigRepo{db: db}
}

func (r *OrganizationSSOConfigRepo) Upsert(ctx context.Context, cfg *entities.OrganizationSSOConfig) error {
	model := OrganizationSSOConfigModelFromEntity(cfg)
	// Save will INSERT on new PK or UPDATE on existing PK.
	if err := r.db.WithContext(ctx).Save(model).Error; err != nil {
		return fmt.Errorf("failed to upsert sso config: %w", err)
	}
	return nil
}

func (r *OrganizationSSOConfigRepo) GetByOrganizationID(ctx context.Context, orgID uuid.UUID) (*entities.OrganizationSSOConfig, error) {
	var model OrganizationSSOConfigModel
	err := r.db.WithContext(ctx).First(&model, "organization_id = ?", orgID.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get sso config: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *OrganizationSSOConfigRepo) Delete(ctx context.Context, orgID uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&OrganizationSSOConfigModel{}, "organization_id = ?", orgID.String())
	if res.Error != nil {
		return fmt.Errorf("failed to delete sso config: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------
// SSORoleMappingRepo
// -----------------------------------------------------------------------

// SSORoleMappingRepo implements repositories.SSORoleMappingRepository.
type SSORoleMappingRepo struct {
	db *gorm.DB
}

// NewSSORoleMappingRepo creates a new SSORoleMappingRepo.
func NewSSORoleMappingRepo(db *gorm.DB) *SSORoleMappingRepo {
	return &SSORoleMappingRepo{db: db}
}

func (r *SSORoleMappingRepo) Create(ctx context.Context, m *entities.SSORoleMapping) error {
	model := OrganizationSSORoleMappingModelFromEntity(m)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create sso role mapping: %w", err)
	}
	return nil
}

func (r *SSORoleMappingRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.SSORoleMapping, error) {
	var model OrganizationSSORoleMappingModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get sso role mapping: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *SSORoleMappingRepo) ListByOrganization(ctx context.Context, orgID uuid.UUID, opts repositories.SSORoleMappingListFilter) ([]*entities.SSORoleMapping, error) {
	var models []OrganizationSSORoleMappingModel
	q := r.db.WithContext(ctx).Where("organization_id = ?", orgID.String()).Order("priority DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list sso role mappings: %w", err)
	}
	out := make([]*entities.SSORoleMapping, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

func (r *SSORoleMappingRepo) Update(ctx context.Context, m *entities.SSORoleMapping) error {
	model := OrganizationSSORoleMappingModelFromEntity(m)
	res := r.db.WithContext(ctx).Model(&OrganizationSSORoleMappingModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)
	if res.Error != nil {
		return fmt.Errorf("failed to update sso role mapping: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *SSORoleMappingRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&OrganizationSSORoleMappingModel{}, "id = ?", id.String())
	if res.Error != nil {
		return fmt.Errorf("failed to delete sso role mapping: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------
// OIDCLoginStateRepo
// -----------------------------------------------------------------------

// OIDCLoginStateRepo implements repositories.OIDCLoginStateRepository.
type OIDCLoginStateRepo struct {
	db *gorm.DB
}

// NewOIDCLoginStateRepo creates a new OIDCLoginStateRepo.
func NewOIDCLoginStateRepo(db *gorm.DB) *OIDCLoginStateRepo {
	return &OIDCLoginStateRepo{db: db}
}

func (r *OIDCLoginStateRepo) Create(ctx context.Context, s *entities.OIDCLoginState) error {
	model := OIDCLoginStateModelFromEntity(s)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create oidc login state: %w", err)
	}
	return nil
}

func (r *OIDCLoginStateRepo) GetAndDelete(ctx context.Context, state string) (*entities.OIDCLoginState, error) {
	var model OIDCLoginStateModel
	// Use a transaction to make the get+delete atomic.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&model, "state = ?", state).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return repositories.ErrNotFound
			}
			return fmt.Errorf("failed to get oidc login state: %w", err)
		}
		if err := tx.Delete(&OIDCLoginStateModel{}, "state = ?", state).Error; err != nil {
			return fmt.Errorf("failed to delete oidc login state: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.ToEntity(), nil
}

func (r *OIDCLoginStateRepo) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Delete(&OIDCLoginStateModel{}, "expires_at < ?", cutoff.UTC())
	if res.Error != nil {
		return 0, fmt.Errorf("failed to delete expired oidc login states: %w", res.Error)
	}
	return res.RowsAffected, nil
}
