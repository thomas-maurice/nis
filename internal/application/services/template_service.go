package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// Reserved template names — kept in sync with the SSK reserved-name guards
// in pkg/manifest/validate.go. "default" and "system" are auto-created
// per-account constructs at lower levels of the identity tree; allowing
// templates to mint them would surface a confusing collision the moment
// someone applies a template to the wrong place.
var reservedTemplateNames = map[string]struct{}{
	"default": {},
	"system":  {},
}

// Sentinel errors. Wrapped so callers can `errors.Is` regardless of the
// formatted message.
var (
	ErrTemplateNameReserved        = errors.New("template name is reserved")
	ErrTemplateHasDependents       = errors.New("template has dependent scoped signing keys")
	ErrTemplateVersionNotFound     = errors.New("template version not found")
	ErrTemplateRefForeignOperator  = errors.New("template belongs to a different operator")
	ErrScopedKeyNotTemplated       = errors.New("scoped signing key is not pinned to a template")
	// ErrSSKTrackLatestRequiresTemplate is returned when a caller asks to
	// create an SSK with track_latest=true but supplies no template ref.
	// Tracking with nothing to track is meaningless.
	ErrSSKTrackLatestRequiresTemplate = errors.New("track_latest requires a template binding")
	// ErrSSKTrackingLatest is returned when a permission edit lands on an
	// SSK that has track_latest=true. The next auto-bump would silently
	// overwrite the edit; force the operator to disable tracking first
	// (or detach) so the intent is explicit.
	ErrSSKTrackingLatest = errors.New("scoped signing key is tracking latest; disable tracking or detach before editing permissions")
	// ErrSSKTrackLatestDrifted is returned when a caller enables
	// track_latest on a templated SSK whose permissions have drifted
	// from the template version. Either bump-template (clearing drift)
	// or detach + re-attach is required.
	ErrSSKTrackLatestDrifted = errors.New("scoped signing key has drifted from its template; bump-template or detach before enabling track_latest")
)

// TemplateService manages operator-scoped permission templates and their
// application onto scoped signing keys. Templates are immutable-versioned:
// every permission edit creates a new template_versions row; pinned SSKs
// stay on their pinned version until an explicit bump.
//
// The service intentionally does NOT call ClusterService directly — push
// happens through the SSK service's BumpScopedKeyTemplate /
// DetachScopedKeyTemplate path (those reuse the existing
// regenerateAccountJWTTx + post-commit push pattern). This service owns
// the template entity; SSK ownership stays with ScopedSigningKeyService.
type TemplateService struct {
	factory     persistence.RepositoryFactory
	permService *PermissionService
	// sskService is the bridge for auto-track propagation: it owns the
	// account-JWT regen helper and the cluster push. Set via
	// WithSSKService post-construction; until then UpdateTemplate
	// silently skips the auto-track fan-out (tests that don't care
	// about that path don't need to wire it).
	sskService *ScopedSigningKeyService
}

func NewTemplateService(factory persistence.RepositoryFactory, permService *PermissionService) *TemplateService {
	return &TemplateService{factory: factory, permService: permService}
}

// WithSSKService attaches the SSK service so UpdateTemplate can drive
// auto-track propagation (snapshot new version onto every
// track_latest=true SSK, regen the parent account JWT, push to
// clusters after commit). Wired post-construction in serve.go because
// TemplateService and ScopedSigningKeyService are mutually independent
// at construction time.
func (s *TemplateService) WithSSKService(ssk *ScopedSigningKeyService) *TemplateService {
	s.sskService = ssk
	return s
}

// CreateTemplateRequest carries the v1 content for a new template.
// Permissions are required (template with no permissions is a footgun);
// ChangeNote is optional.
type CreateTemplateRequest struct {
	OperatorID      uuid.UUID
	Name            string
	Description     string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs int
	ResponseTTL     time.Duration
	ChangeNote      string
	CreatedByUserID *uuid.UUID
}

// CreateTemplate creates the parent template row plus its v1
// template_versions snapshot in a single tx, then emits template.created.
// Returns the materialised template and version.
func (s *TemplateService) CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*entities.Template, *entities.TemplateVersion, error) {
	if req.Name == "" {
		return nil, nil, fmt.Errorf("template name is required")
	}
	if _, reserved := reservedTemplateNames[req.Name]; reserved {
		return nil, nil, fmt.Errorf("%w: %q", ErrTemplateNameReserved, req.Name)
	}

	var (
		outTpl *entities.Template
		outVer *entities.TemplateVersion
	)
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		// Verify operator exists (FK would catch this on Create too, but a
		// cleaner error message saves the caller a round-trip).
		if _, err := tx.OperatorRepository().GetByID(ctx, req.OperatorID); err != nil {
			return fmt.Errorf("get operator: %w", err)
		}
		// Pre-check uniqueness for a friendlier error than the FK violation.
		existing, err := tx.TemplateRepository().GetByName(ctx, req.OperatorID, req.Name)
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return fmt.Errorf("check existing template: %w", err)
		}
		if existing != nil {
			return repositories.ErrAlreadyExists
		}

		now := clock.Now()
		tpl := &entities.Template{
			ID:            uuid.New(),
			OperatorID:    req.OperatorID,
			Name:          req.Name,
			Description:   req.Description,
			LatestVersion: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := tx.TemplateRepository().Create(ctx, tpl); err != nil {
			return fmt.Errorf("create template: %w", err)
		}

		ver := &entities.TemplateVersion{
			ID:              uuid.New(),
			TemplateID:      tpl.ID,
			VersionNumber:   1,
			PubAllow:        req.PubAllow,
			PubDeny:         req.PubDeny,
			SubAllow:        req.SubAllow,
			SubDeny:         req.SubDeny,
			ResponseMaxMsgs: req.ResponseMaxMsgs,
			ResponseTTL:     req.ResponseTTL,
			ChangeNote:      req.ChangeNote,
			CreatedAt:       now,
			CreatedByUserID: req.CreatedByUserID,
		}
		if err := tx.TemplateVersionRepository().Create(ctx, ver); err != nil {
			return fmt.Errorf("create template version v1: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeTemplateCreated,
			OperatorID:   &tpl.OperatorID,
			ResourceType: "template",
			ResourceID:   tpl.ID.String(),
			Payload:      map[string]any{"name": tpl.Name, "version": 1},
		}); err != nil {
			return fmt.Errorf("emit template.created: %w", err)
		}

		outTpl, outVer = tpl, ver
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return outTpl, outVer, nil
}

// GetTemplate returns the template plus the requested version. When
// versionNumber == 0, returns the latest.
func (s *TemplateService) GetTemplate(ctx context.Context, id uuid.UUID, versionNumber int) (*entities.Template, *entities.TemplateVersion, error) {
	tpl, err := s.factory.TemplateRepository().GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	target := versionNumber
	if target == 0 {
		target = tpl.LatestVersion
	}
	ver, err := s.factory.TemplateVersionRepository().GetByTemplateAndNumber(ctx, id, target)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return tpl, nil, fmt.Errorf("%w: template %s version %d", ErrTemplateVersionNotFound, id, target)
		}
		return nil, nil, err
	}
	return tpl, ver, nil
}

// GetTemplateByName mirrors GetTemplate for the (operator, name) lookup
// path used by manifests and nisctl.
func (s *TemplateService) GetTemplateByName(ctx context.Context, operatorID uuid.UUID, name string, versionNumber int) (*entities.Template, *entities.TemplateVersion, error) {
	tpl, err := s.factory.TemplateRepository().GetByName(ctx, operatorID, name)
	if err != nil {
		return nil, nil, err
	}
	return s.GetTemplate(ctx, tpl.ID, versionNumber)
}

func (s *TemplateService) ListTemplates(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Template, error) {
	return s.factory.TemplateRepository().ListByOperator(ctx, operatorID, opts)
}

func (s *TemplateService) ListTemplateVersions(ctx context.Context, templateID uuid.UUID) ([]*entities.TemplateVersion, error) {
	return s.factory.TemplateVersionRepository().ListByTemplate(ctx, templateID)
}

func (s *TemplateService) ListDependents(ctx context.Context, templateID uuid.UUID) ([]*entities.ScopedSigningKey, error) {
	return s.factory.TemplateRepository().ListDependentScopedKeys(ctx, templateID)
}

// UpdateTemplateRequest. When any of the permission/response fields is
// non-nil AND the resolved permission set differs from the current
// latest_version, a new template_versions row is created and
// latest_version is bumped. Description-only edits do not bump.
//
// Pointer semantics matter: nil = "leave alone", non-nil empty slice =
// "set to empty". Same shape as UpdateScopedSigningKeyRequest.
type UpdateTemplateRequest struct {
	Description     *string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs *int
	ResponseTTL     *time.Duration
	ChangeNote      string
	CreatedByUserID *uuid.UUID

	// PermissionsProvided is set true when the caller is touching the
	// permission surface at all. Without it we can't distinguish "leave
	// permissions alone" from "set everything to empty". Handlers set this
	// based on whether the request carried a permissions block.
	PermissionsProvided bool
}

// autoTrackCap caps how many tracking SSKs UpdateTemplate will fan out
// to in one shot. Past this the call returns FailedPrecondition with a
// hint to disable tracking on the excess SSKs first. Hard cap rather
// than queued because (a) we have no worker substrate and (b) holding
// row locks on hundreds of accounts in one tx would serialise unrelated
// account-JWT regens. Tunable later if it ever bites.
const autoTrackCap = 200

func (s *TemplateService) UpdateTemplate(ctx context.Context, id uuid.UUID, req UpdateTemplateRequest) (*entities.Template, *entities.TemplateVersion, error) {
	var (
		outTpl       *entities.Template
		outVer       *entities.TemplateVersion
		pushAccounts []uuid.UUID
	)
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		tpl, err := tx.TemplateRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}

		latest, err := tx.TemplateVersionRepository().GetByTemplateAndNumber(ctx, id, tpl.LatestVersion)
		if err != nil {
			return fmt.Errorf("get latest version: %w", err)
		}

		dirtyMeta := false
		if req.Description != nil && *req.Description != tpl.Description {
			tpl.Description = *req.Description
			dirtyMeta = true
		}

		// Compose the candidate permission set, falling back to the latest
		// version's values for fields the caller didn't touch.
		candidate := *latest
		candidate.ID = uuid.UUID{} // re-stamped below if we bump
		candidate.ChangeNote = req.ChangeNote
		candidate.CreatedByUserID = req.CreatedByUserID
		if req.PermissionsProvided {
			if req.PubAllow != nil {
				candidate.PubAllow = req.PubAllow
			}
			if req.PubDeny != nil {
				candidate.PubDeny = req.PubDeny
			}
			if req.SubAllow != nil {
				candidate.SubAllow = req.SubAllow
			}
			if req.SubDeny != nil {
				candidate.SubDeny = req.SubDeny
			}
			if req.ResponseMaxMsgs != nil {
				candidate.ResponseMaxMsgs = *req.ResponseMaxMsgs
			}
			if req.ResponseTTL != nil {
				candidate.ResponseTTL = *req.ResponseTTL
			}
		}

		shouldBump := req.PermissionsProvided && !permissionSetsEqual(&candidate, latest)

		if shouldBump {
			now := clock.Now()
			candidate.ID = uuid.New()
			candidate.TemplateID = tpl.ID
			candidate.VersionNumber = tpl.LatestVersion + 1
			candidate.CreatedAt = now
			if err := tx.TemplateVersionRepository().Create(ctx, &candidate); err != nil {
				return fmt.Errorf("create template version v%d: %w", candidate.VersionNumber, err)
			}
			tpl.LatestVersion = candidate.VersionNumber
			outVer = &candidate
			dirtyMeta = true

			// Auto-track fan-out. List all SSKs tracking this template,
			// snapshot the new version onto them, bump their template_version,
			// and queue their parent accounts for a post-commit JWT push. The
			// per-account JWT regen happens once per unique account inside
			// this tx — multiple tracking SSKs on the same account are
			// coalesced. Skip silently when sskService isn't wired (mostly
			// unit tests; the prod path always wires it in serve.go).
			if s.sskService != nil {
				tracking, err := tx.TemplateRepository().ListTrackingScopedKeys(ctx, tpl.ID)
				if err != nil {
					return fmt.Errorf("list tracking scoped keys: %w", err)
				}
				if len(tracking) > autoTrackCap {
					return fmt.Errorf("auto-track fan-out exceeds cap (%d > %d); disable tracking on excess scoped keys before bumping this template",
						len(tracking), autoTrackCap)
				}
				touchedAccounts := make(map[uuid.UUID]struct{}, len(tracking))
				for _, ssk := range tracking {
					ssk.PubAllow = candidate.PubAllow
					ssk.PubDeny = candidate.PubDeny
					ssk.SubAllow = candidate.SubAllow
					ssk.SubDeny = candidate.SubDeny
					ssk.ResponseMaxMsgs = candidate.ResponseMaxMsgs
					ssk.ResponseTTL = candidate.ResponseTTL
					v := candidate.VersionNumber
					ssk.TemplateVersion = &v
					ssk.TemplateDrifted = false
					ssk.UpdatedAt = now
					if err := tx.ScopedSigningKeyRepository().Update(ctx, ssk); err != nil {
						return fmt.Errorf("auto-track update SSK %s: %w", ssk.ID, err)
					}
					if err := events.EmitTx(ctx, tx, events.Event{
						Type:         entities.EventTypeTemplateApplied,
						OperatorID:   &tpl.OperatorID,
						AccountID:    &ssk.AccountID,
						ResourceType: "template",
						ResourceID:   tpl.ID.String(),
						Payload: map[string]any{
							"action":           "auto_track",
							"scoped_key_id":    ssk.ID.String(),
							"scoped_key_name":  ssk.Name,
							"template_version": v,
						},
					}); err != nil {
						return fmt.Errorf("emit template.applied_to_scoped_key (auto_track): %w", err)
					}
					if err := events.EmitTx(ctx, tx, events.Event{
						Type:         entities.EventTypeScopedKeyUpdated,
						OperatorID:   &tpl.OperatorID,
						AccountID:    &ssk.AccountID,
						ResourceType: "scoped_key",
						ResourceID:   ssk.ID.String(),
						Payload: map[string]any{
							"name":              ssk.Name,
							"auto_tracked":      true,
							"bumped_to_version": v,
						},
					}); err != nil {
						return fmt.Errorf("emit scoped_key.updated (auto_track): %w", err)
					}
					if _, seen := touchedAccounts[ssk.AccountID]; seen {
						continue
					}
					touchedAccounts[ssk.AccountID] = struct{}{}
					if err := s.sskService.RegenerateAccountJWTTx(ctx, tx, ssk.AccountID); err != nil {
						return fmt.Errorf("auto-track regen account %s JWT: %w", ssk.AccountID, err)
					}
					pushAccounts = append(pushAccounts, ssk.AccountID)
				}
			}
		}

		if dirtyMeta {
			tpl.UpdatedAt = clock.Now()
			if err := tx.TemplateRepository().Update(ctx, tpl); err != nil {
				return fmt.Errorf("update template: %w", err)
			}
			payload := map[string]any{"name": tpl.Name, "version": tpl.LatestVersion, "bumped": shouldBump}
			if err := events.EmitTx(ctx, tx, events.Event{
				Type:         entities.EventTypeTemplateUpdated,
				OperatorID:   &tpl.OperatorID,
				ResourceType: "template",
				ResourceID:   tpl.ID.String(),
				Payload:      payload,
			}); err != nil {
				return fmt.Errorf("emit template.updated: %w", err)
			}
		}

		outTpl = tpl
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// Best-effort cluster pushes for every account that had at least one
	// tracking SSK auto-bumped. Same A13-lite semantic as
	// ScopedSigningKeyService.pushAccountAfterCommit — failures are
	// logged but do not fail the API call; nisctl cluster sync is the
	// recovery tool.
	if s.sskService != nil {
		for _, accountID := range pushAccounts {
			s.sskService.PushAccountAfterCommit(ctx, accountID)
		}
	}
	return outTpl, outVer, nil
}

// DeleteTemplate refuses when any SSK pins this template. The FK on the
// SSK side is ON DELETE SET NULL (not RESTRICT) — see the schema note
// for why — so this app-layer guard is the only block.
func (s *TemplateService) DeleteTemplate(ctx context.Context, id uuid.UUID) error {
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		tpl, err := tx.TemplateRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}
		n, err := tx.TemplateRepository().CountDependentScopedKeys(ctx, id)
		if err != nil {
			return fmt.Errorf("count dependents: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("%w (%d)", ErrTemplateHasDependents, n)
		}
		if err := tx.TemplateRepository().Delete(ctx, id); err != nil {
			return err
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeTemplateDeleted,
			OperatorID:   &tpl.OperatorID,
			ResourceType: "template",
			ResourceID:   tpl.ID.String(),
			Payload:      map[string]any{"name": tpl.Name, "last_version": tpl.LatestVersion},
		}); err != nil {
			return fmt.Errorf("emit template.deleted: %w", err)
		}
		return nil
	})
}

// permissionSetsEqual compares the permission-shape fields between two
// versions; ignores ID/TemplateID/VersionNumber/timestamps/change_note/
// created_by_user_id (those are version-row metadata, not the snapshot).
func permissionSetsEqual(a, b *entities.TemplateVersion) bool {
	if a.ResponseMaxMsgs != b.ResponseMaxMsgs || a.ResponseTTL != b.ResponseTTL {
		return false
	}
	return stringSlicesEqual(a.PubAllow, b.PubAllow) &&
		stringSlicesEqual(a.PubDeny, b.PubDeny) &&
		stringSlicesEqual(a.SubAllow, b.SubAllow) &&
		stringSlicesEqual(a.SubDeny, b.SubDeny)
}

// stringSlicesEqual compares two []string for value equality. Treats nil
// and []string{} as equal — both mean "no entries" in the manifest /
// proto round-trip and we don't want a roundtrip through an empty proto
// field to register as a permission change.
func stringSlicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
