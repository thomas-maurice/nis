package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// TemplateRepository defines persistence for operator-scoped permission
// templates. Version rows live in TemplateVersionRepository.
type TemplateRepository interface {
	Create(ctx context.Context, t *entities.Template) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Template, error)
	GetByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Template, error)
	ListByOperator(ctx context.Context, operatorID uuid.UUID, opts ListOptions) ([]*entities.Template, error)
	List(ctx context.Context, opts ListOptions) ([]*entities.Template, error)
	Update(ctx context.Context, t *entities.Template) error
	Delete(ctx context.Context, id uuid.UUID) error

	// CountDependentScopedKeys returns how many SSK rows currently pin
	// this template. Used by DeleteTemplate to reject when > 0 with a
	// FailedPrecondition.
	CountDependentScopedKeys(ctx context.Context, templateID uuid.UUID) (int64, error)

	// ListDependentScopedKeys returns the SSKs pinned to this template
	// across all accounts under the operator. Used by the UI/CLI to show
	// who would be affected by a bump or a delete attempt.
	ListDependentScopedKeys(ctx context.Context, templateID uuid.UUID) ([]*entities.ScopedSigningKey, error)

	// ListTrackingScopedKeys returns the SSKs pinned to this template
	// that have track_latest=true. Used by TemplateService.UpdateTemplate
	// to find the set of SSKs that should be auto-bumped to a newly-
	// created version. Ordered by account_id then name for stable
	// per-account batching.
	ListTrackingScopedKeys(ctx context.Context, templateID uuid.UUID) ([]*entities.ScopedSigningKey, error)
}

// TemplateVersionRepository persists immutable permission snapshots for
// a template. Updates always insert a new row; rows are never mutated.
type TemplateVersionRepository interface {
	Create(ctx context.Context, v *entities.TemplateVersion) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.TemplateVersion, error)
	GetByTemplateAndNumber(ctx context.Context, templateID uuid.UUID, version int) (*entities.TemplateVersion, error)
	ListByTemplate(ctx context.Context, templateID uuid.UUID) ([]*entities.TemplateVersion, error)
}
