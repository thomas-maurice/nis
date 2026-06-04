package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

var slugRegexp = regexp.MustCompile(`^[a-z0-9-]+$`)

// OrganizationService manages organizations and their SSO configuration.
type OrganizationService struct {
	factory   persistence.RepositoryFactory
	encryptor encryption.Encryptor
}

func NewOrganizationService(factory persistence.RepositoryFactory, encryptor encryption.Encryptor) *OrganizationService {
	return &OrganizationService{factory: factory, encryptor: encryptor}
}

// CreateOrganizationRequest is the input to OrganizationService.CreateOrganization.
type CreateOrganizationRequest struct {
	Name        string
	Slug        string
	Description string
}

// UpdateOrganizationRequest is the input to OrganizationService.UpdateOrganization.
// Slug is immutable and must not appear here.
type UpdateOrganizationRequest struct {
	Name        string
	Description string
}

// SetSSOConfigRequest is the input to OrganizationService.SetSSOConfig.
// ClientSecret is plaintext; an empty value means "leave existing secret unchanged".
type SetSSOConfigRequest struct {
	OrganizationID uuid.UUID
	Enabled        bool
	IssuerURL      string
	ClientID       string
	ClientSecret   string // plaintext; "" = leave unchanged
	Scopes         string
	GroupClaim     string
	DefaultRole    *entities.APIUserRole
}

// SSORoleMappingInput is the write shape for a single role mapping.
type SSORoleMappingInput struct {
	GroupValue      string
	Role            entities.APIUserRole
	ScopeOperatorID *uuid.UUID
	ScopeAccountID  *uuid.UUID
	Priority        int
}

// CreateOrganization validates and creates a new organization.
func (s *OrganizationService) CreateOrganization(ctx context.Context, req CreateOrganizationRequest) (*entities.Organization, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("organization name is required")
	}
	if req.Slug == "" {
		return nil, fmt.Errorf("organization slug is required")
	}
	if !slugRegexp.MatchString(req.Slug) {
		return nil, fmt.Errorf("slug must match [a-z0-9-]+, got %q", req.Slug)
	}

	now := clock.Now()
	org := &entities.Organization{
		ID:          uuid.New(),
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OrganizationRepository().Create(ctx, org); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOrganizationCreated,
			ResourceType: "organization",
			ResourceID:   org.ID.String(),
			Payload: map[string]any{
				"name": org.Name,
				"slug": org.Slug,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return org, nil
}

// GetOrganization retrieves an organization by ID.
func (s *OrganizationService) GetOrganization(ctx context.Context, id uuid.UUID) (*entities.Organization, error) {
	return s.factory.OrganizationRepository().GetByID(ctx, id)
}

// GetOrganizationBySlug retrieves an organization by slug.
func (s *OrganizationService) GetOrganizationBySlug(ctx context.Context, slug string) (*entities.Organization, error) {
	return s.factory.OrganizationRepository().GetBySlug(ctx, slug)
}

// ListOrganizations retrieves all organizations with basic pagination.
func (s *OrganizationService) ListOrganizations(ctx context.Context, opts repositories.ListOptions) ([]*entities.Organization, error) {
	return s.factory.OrganizationRepository().List(ctx, opts)
}

// UpdateOrganization updates the name and description of an organization (slug is immutable).
func (s *OrganizationService) UpdateOrganization(ctx context.Context, id uuid.UUID, req UpdateOrganizationRequest) (*entities.Organization, error) {
	var result *entities.Organization
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		org, err := tx.OrganizationRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}

		beforeName := org.Name
		beforeDescription := org.Description

		org.Name = req.Name
		org.Description = req.Description
		org.UpdatedAt = clock.Now()

		if err := tx.OrganizationRepository().Update(ctx, org); err != nil {
			return err
		}

		var diff events.DiffBuilder
		diff.Set("name", beforeName, org.Name)
		diff.Set("description", beforeDescription, org.Description)

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOrganizationUpdated,
			ResourceType: "organization",
			ResourceID:   org.ID.String(),
			Payload: map[string]any{
				"name": org.Name,
				"slug": org.Slug,
			},
			Diff: diff.Finalize(),
		}); err != nil {
			return fmt.Errorf("emit organization.updated: %w", err)
		}

		result = org
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteOrganization deletes an organization. Refuses to delete the default org.
func (s *OrganizationService) DeleteOrganization(ctx context.Context, id uuid.UUID) error {
	if id == uuid.MustParse(entities.DefaultOrganizationID) {
		return fmt.Errorf("cannot delete the default organization")
	}
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OrganizationRepository().Delete(ctx, id); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOrganizationDeleted,
			ResourceType: "organization",
			ResourceID:   id.String(),
		})
	})
}

// GetSSOConfig retrieves the SSO config for an organization.
func (s *OrganizationService) GetSSOConfig(ctx context.Context, orgID uuid.UUID) (*entities.OrganizationSSOConfig, error) {
	return s.factory.OrganizationSSOConfigRepository().GetByOrganizationID(ctx, orgID)
}

// SetSSOConfig creates or updates the SSO configuration for an organization.
func (s *OrganizationService) SetSSOConfig(ctx context.Context, req SetSSOConfigRequest) (*entities.OrganizationSSOConfig, error) {
	if req.DefaultRole != nil && *req.DefaultRole == entities.RoleAdmin {
		return nil, fmt.Errorf("SSO default role cannot be admin")
	}

	// Fetch existing config (may not exist yet).
	existing, err := s.factory.OrganizationSSOConfigRepository().GetByOrganizationID(ctx, req.OrganizationID)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, fmt.Errorf("fetch existing SSO config: %w", err)
	}

	secretChanged := req.ClientSecret != ""

	var encryptedSecret string
	if secretChanged {
		enc, err := s.encryptor.Encrypt(ctx, []byte(req.ClientSecret))
		if err != nil {
			return nil, fmt.Errorf("encrypt client secret: %w", err)
		}
		encryptedSecret = enc
	} else if existing != nil {
		encryptedSecret = existing.EncryptedClientSecret
	}

	now := clock.Now()
	cfg := &entities.OrganizationSSOConfig{
		OrganizationID:        req.OrganizationID,
		Enabled:               req.Enabled,
		IssuerURL:             req.IssuerURL,
		ClientID:              req.ClientID,
		EncryptedClientSecret: encryptedSecret,
		Scopes:                req.Scopes,
		GroupClaim:            req.GroupClaim,
		DefaultRole:           req.DefaultRole,
		UpdatedAt:             now,
	}

	if existing != nil {
		cfg.ID = existing.ID
		cfg.CreatedAt = existing.CreatedAt
	} else {
		cfg.ID = uuid.New()
		cfg.CreatedAt = now
	}

	var result *entities.OrganizationSSOConfig
	err = s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OrganizationSSOConfigRepository().Upsert(ctx, cfg); err != nil {
			return err
		}

		var diff events.DiffBuilder
		diff.SetRedacted("client_secret", secretChanged)
		diff.Set("enabled", func() bool {
			if existing != nil {
				return existing.Enabled
			}
			return false
		}(), cfg.Enabled)
		diff.Set("issuer_url", func() string {
			if existing != nil {
				return existing.IssuerURL
			}
			return ""
		}(), cfg.IssuerURL)
		diff.Set("client_id", func() string {
			if existing != nil {
				return existing.ClientID
			}
			return ""
		}(), cfg.ClientID)

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOrganizationSSOConfigured,
			ResourceType: "organization",
			ResourceID:   req.OrganizationID.String(),
			Payload: map[string]any{
				"enabled":    cfg.Enabled,
				"issuer_url": cfg.IssuerURL,
				"client_id":  cfg.ClientID,
			},
			Diff: diff.Finalize(),
		}); err != nil {
			return fmt.Errorf("emit organization.sso.configured: %w", err)
		}

		result = cfg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteSSOConfig removes the SSO configuration for an organization.
func (s *OrganizationService) DeleteSSOConfig(ctx context.Context, orgID uuid.UUID) error {
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OrganizationSSOConfigRepository().Delete(ctx, orgID); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOrganizationSSOConfigDeleted,
			ResourceType: "organization",
			ResourceID:   orgID.String(),
		})
	})
}

// ListSSORoleMappings returns all role mappings for an organization.
func (s *OrganizationService) ListSSORoleMappings(ctx context.Context, orgID uuid.UUID) ([]*entities.SSORoleMapping, error) {
	return s.factory.SSORoleMappingRepository().ListByOrganization(ctx, orgID, repositories.SSORoleMappingListFilter{})
}

// SetSSORoleMappings replaces all role mappings for an organization atomically.
func (s *OrganizationService) SetSSORoleMappings(ctx context.Context, orgID uuid.UUID, inputs []SSORoleMappingInput) ([]*entities.SSORoleMapping, error) {
	for _, inp := range inputs {
		if inp.GroupValue == "" {
			return nil, fmt.Errorf("group_value is required for each mapping")
		}
		switch inp.Role {
		case entities.RoleOrgAdmin, entities.RoleOperatorAdmin, entities.RoleAccountAdmin:
			// valid
		default:
			return nil, fmt.Errorf("role %q is not allowed in SSO mappings (must be org-admin, operator-admin, or account-admin)", inp.Role)
		}
		if inp.Role == entities.RoleOperatorAdmin && inp.ScopeOperatorID == nil {
			return nil, fmt.Errorf("scope_operator_id is required when role is operator-admin")
		}
		if inp.Role == entities.RoleAccountAdmin && inp.ScopeAccountID == nil {
			return nil, fmt.Errorf("scope_account_id is required when role is account-admin")
		}
	}

	var result []*entities.SSORoleMapping
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		// Fetch and delete existing mappings.
		existing, err := tx.SSORoleMappingRepository().ListByOrganization(ctx, orgID, repositories.SSORoleMappingListFilter{})
		if err != nil {
			return fmt.Errorf("list existing mappings: %w", err)
		}
		for _, m := range existing {
			if err := tx.SSORoleMappingRepository().Delete(ctx, m.ID); err != nil {
				return fmt.Errorf("delete mapping %s: %w", m.ID, err)
			}
		}

		// Create new mappings.
		now := clock.Now()
		var created []*entities.SSORoleMapping
		for _, inp := range inputs {
			m := &entities.SSORoleMapping{
				ID:              uuid.New(),
				OrganizationID:  orgID,
				GroupValue:      inp.GroupValue,
				Role:            inp.Role,
				ScopeOperatorID: inp.ScopeOperatorID,
				ScopeAccountID:  inp.ScopeAccountID,
				Priority:        inp.Priority,
				CreatedAt:       now,
			}
			if err := tx.SSORoleMappingRepository().Create(ctx, m); err != nil {
				return fmt.Errorf("create mapping: %w", err)
			}
			created = append(created, m)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOrganizationSSOMappingsUpdated,
			ResourceType: "organization",
			ResourceID:   orgID.String(),
			Payload: map[string]any{
				"count": len(inputs),
			},
		}); err != nil {
			return fmt.Errorf("emit organization.sso.mappings_updated: %w", err)
		}

		result = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
