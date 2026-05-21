package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ScopedSigningKeyService provides business logic for scoped signing key management.
//
// Every mutating method (Create / Update / Delete) re-signs the parent account's JWT
// so the NATS resolver sees the up-to-date set of scoped signers. Without that, NATS
// rejects users signed by newly-created or just-modified scoped keys as
// "Authorization Violation" — the bug previously labelled E1 in PROPOSALS.md.
//
// The key mutation and the account-JWT regen share a single tx via
// factory.WithTx, so a JWT-regen failure rolls back the key mutation too. That
// prevents the account-JWT-references-missing-key (or missing-JWT-references-existing-key)
// split-brain state that previously required best-effort manual rollback.
type ScopedSigningKeyService struct {
	factory        persistence.RepositoryFactory
	jwtService     *JWTService
	encryptor      encryption.Encryptor
	clusterService *ClusterService // optional; set via WithClusterService for post-commit NATS pushes
}

// NewScopedSigningKeyService creates a new scoped signing key service
func NewScopedSigningKeyService(
	factory persistence.RepositoryFactory,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *ScopedSigningKeyService {
	return &ScopedSigningKeyService{
		factory:    factory,
		jwtService: jwtService,
		encryptor:  encryptor,
	}
}

// WithClusterService attaches a ClusterService used to push the
// parent account JWT to every attached cluster after an SSK mutation
// commits. Mirrors AccountService.WithClusterService — both are wired
// post-construction in serve.go because the two services are mutually
// independent at construction time. Tests that don't wire this skip the
// NATS push; the DB mutation, JWT regen, and audit event still happen.
func (s *ScopedSigningKeyService) WithClusterService(cs *ClusterService) *ScopedSigningKeyService {
	s.clusterService = cs
	return s
}

// PushAccountAfterCommit loads the parent account (with its freshly
// regenerated JWT) and pushes it to every cluster attached to its
// operator. Best-effort: per-cluster failures are logged and the call
// returns. Mirrors AccountService.pushAccountAfterCommit; see there for
// the rationale (DB is source of truth, drift dashboard surfaces lag).
// Exported so TemplateService can reuse it for auto-track propagation
// without duplicating the per-cluster fan-out + error-logging pattern.
func (s *ScopedSigningKeyService) PushAccountAfterCommit(ctx context.Context, accountID uuid.UUID) {
	s.pushAccountAfterCommit(ctx, accountID)
}

func (s *ScopedSigningKeyService) pushAccountAfterCommit(ctx context.Context, accountID uuid.UUID) {
	if s.clusterService == nil {
		return
	}
	account, err := s.factory.AccountRepository().GetByID(ctx, accountID)
	if err != nil {
		logging.LogFromContext(ctx).Warn("scoped-key auto-sync: load account failed; manual 'nisctl cluster sync' will reconcile",
			"account_id", accountID,
			"error", err,
		)
		return
	}
	errs := s.clusterService.PushAccountToAllClusters(ctx, account.OperatorID, account)
	if len(errs) == 0 {
		return
	}
	log := logging.LogFromContext(ctx)
	for _, e := range errs {
		log.Warn("scoped-key auto-sync: account JWT push failed; run 'nisctl cluster sync' to reconcile",
			"account", account.Name,
			"account_public_key", account.PublicKey,
			"error", e.Error,
		)
	}
}

// RegenerateAccountJWTTx is the exported wrapper around regenerateAccountJWTTx.
// TemplateService calls this from its auto-track propagation path so the
// same tx that updates the tracking SSKs also re-signs the parent account
// JWT — keeping the "account JWT row mirrors the current set of scoped
// signers" invariant atomic across services.
func (s *ScopedSigningKeyService) RegenerateAccountJWTTx(ctx context.Context, tx persistence.RepositoryFactory, accountID uuid.UUID) error {
	return s.regenerateAccountJWTTx(ctx, tx, accountID)
}

func (s *ScopedSigningKeyService) regenerateAccountJWTTx(ctx context.Context, tx persistence.RepositoryFactory, accountID uuid.UUID) error {
	accountRepo := tx.AccountRepository()
	operatorRepo := tx.OperatorRepository()
	scopedKeyRepo := tx.ScopedSigningKeyRepository()

	account, err := accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}
	scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, accountID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return fmt.Errorf("failed to list scoped signing keys: %w", err)
	}
	revs, err := tx.UserJWTRevocationRepository().ListActiveByAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to list active revocations: %w", err)
	}
	newJWT, err := s.jwtService.GenerateAccountJWT(ctx, account, operator, scopedKeys, revs, operator.AccountJWTTTL)
	if err != nil {
		return fmt.Errorf("failed to regenerate account JWT: %w", err)
	}
	account.JWT = newJWT
	account.UpdatedAt = clock.Now()
	if err := accountRepo.Update(ctx, account); err != nil {
		return fmt.Errorf("failed to persist regenerated account JWT: %w", err)
	}
	return nil
}

// CreateScopedSigningKeyRequest contains the data needed to create a scoped signing key
type CreateScopedSigningKeyRequest struct {
	AccountID       uuid.UUID
	Name            string
	Description     string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs int
	ResponseTTL     time.Duration
	// Optional template ref. When set, Pub*/Sub*/Response* above are
	// ignored and the named template's permissions are snapshotted into
	// the SSK columns. Cross-operator refs are rejected with
	// ErrTemplateRefForeignOperator so an admin can't accidentally seed
	// operator B's account with operator A's template.
	TemplateRef *TemplateRef
	// TrackLatest opts the new SSK into TemplateService.UpdateTemplate's
	// auto-propagation. Only honoured when TemplateRef != nil; rejected
	// otherwise. When set, the SSK is snapshotted from the template's
	// current latest_version regardless of any VersionNumber pin — a
	// version pin + tracking-latest contradict.
	TrackLatest bool
}

// TemplateRef names a template at create-from-template time. Version 0
// resolves to the template's current latest_version.
type TemplateRef struct {
	OperatorID    uuid.UUID
	TemplateName  string
	VersionNumber int
}

// CreateScopedSigningKey creates a new scoped signing key with generated keys
func (s *ScopedSigningKeyService) CreateScopedSigningKey(ctx context.Context, req CreateScopedSigningKeyRequest) (*entities.ScopedSigningKey, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("scoped signing key name is required")
	}

	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		scopedKeyRepo := tx.ScopedSigningKeyRepository()
		accountRepo := tx.AccountRepository()

		// Get account to verify it exists and capture OperatorID for event emission
		account, err := accountRepo.GetByID(ctx, req.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}

		// Check if scoped key with this name already exists for this account
		existing, err := scopedKeyRepo.GetByName(ctx, req.AccountID, req.Name)
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return fmt.Errorf("failed to check existing scoped signing key: %w", err)
		}
		if existing != nil {
			return repositories.ErrAlreadyExists
		}

		// Generate account NKey pair (scoped signing keys use account key prefix)
		seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
		if err != nil {
			return fmt.Errorf("failed to generate scoped signing key: %w", err)
		}

		// Encrypt the seed
		encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
		if err != nil {
			return fmt.Errorf("failed to encrypt scoped signing key seed: %w", err)
		}

		// Resolve permissions: template ref wins over caller-supplied
		// pub/sub fields. Cross-operator ref is a hard error — admins
		// could otherwise seed operator B's account with operator A's
		// template, which is silently confusing and surfaces only on the
		// next account-JWT regen.
		var (
			pubAllow, pubDeny  = req.PubAllow, req.PubDeny
			subAllow, subDeny  = req.SubAllow, req.SubDeny
			respMax            = req.ResponseMaxMsgs
			respTTL            = req.ResponseTTL
			tmplID             *uuid.UUID
			tmplVer            *int
		)
		if req.TemplateRef != nil {
			if req.TemplateRef.OperatorID != account.OperatorID {
				return fmt.Errorf("%w: account belongs to operator %s, template ref names operator %s",
					ErrTemplateRefForeignOperator, account.OperatorID, req.TemplateRef.OperatorID)
			}
			tpl, err := tx.TemplateRepository().GetByName(ctx, req.TemplateRef.OperatorID, req.TemplateRef.TemplateName)
			if err != nil {
				return fmt.Errorf("resolve template %q: %w", req.TemplateRef.TemplateName, err)
			}
			// When TrackLatest is requested we always pin to the current
			// latest version, ignoring any caller-supplied VersionNumber.
			// A pin + tracking-latest is contradictory: the next bump
			// would silently move past the pin.
			target := req.TemplateRef.VersionNumber
			if req.TrackLatest || target == 0 {
				target = tpl.LatestVersion
			}
			ver, err := tx.TemplateVersionRepository().GetByTemplateAndNumber(ctx, tpl.ID, target)
			if err != nil {
				if errors.Is(err, repositories.ErrNotFound) {
					return fmt.Errorf("%w: template %s version %d", ErrTemplateVersionNotFound, tpl.Name, target)
				}
				return fmt.Errorf("get template version: %w", err)
			}
			pubAllow, pubDeny = ver.PubAllow, ver.PubDeny
			subAllow, subDeny = ver.SubAllow, ver.SubDeny
			respMax, respTTL = ver.ResponseMaxMsgs, ver.ResponseTTL
			tid := tpl.ID
			tnum := ver.VersionNumber
			tmplID, tmplVer = &tid, &tnum
		} else if req.TrackLatest {
			return fmt.Errorf("%w: track_latest requires a template ref", ErrSSKTrackLatestRequiresTemplate)
		}

		// Create scoped signing key entity
		scopedKey := &entities.ScopedSigningKey{
			ID:              uuid.New(),
			AccountID:       req.AccountID,
			Name:            req.Name,
			Description:     req.Description,
			EncryptedSeed:   encryptedSeed,
			PublicKey:       pubKey,
			PubAllow:        pubAllow,
			PubDeny:         pubDeny,
			SubAllow:        subAllow,
			SubDeny:         subDeny,
			ResponseMaxMsgs: respMax,
			ResponseTTL:     respTTL,
			TemplateID:      tmplID,
			TemplateVersion: tmplVer,
			TemplateDrifted: false,
			// TrackLatest is only honoured when there's a template ref;
			// the validation above rejects "track without template".
			TrackLatest: req.TrackLatest && tmplID != nil,
			CreatedAt:   clock.Now(),
			UpdatedAt:   clock.Now(),
		}

		// Save to repository
		if err := scopedKeyRepo.Create(ctx, scopedKey); err != nil {
			return fmt.Errorf("failed to create scoped signing key: %w", err)
		}

		// Re-sign the account JWT so NATS recognises the new scoped signer.
		// On failure, WithTx rolls back both the just-persisted key and any
		// account update we made — no manual cleanup needed.
		if err := s.regenerateAccountJWTTx(ctx, tx, req.AccountID); err != nil {
			return err
		}

		createPayload := map[string]any{"name": scopedKey.Name, "public_key": scopedKey.PublicKey}
		if scopedKey.TemplateID != nil {
			createPayload["template_id"] = scopedKey.TemplateID.String()
			createPayload["template_version"] = *scopedKey.TemplateVersion
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyCreated,
			OperatorID:   &account.OperatorID,
			AccountID:    &scopedKey.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   scopedKey.ID.String(),
			Payload:      createPayload,
		}); err != nil {
			return fmt.Errorf("emit scoped_key.created: %w", err)
		}
		// When the SSK was seeded from a template, also emit
		// template.applied_to_scoped_key with action=create so audit
		// queries on the template can find every key that ever adopted a
		// version of it.
		if scopedKey.TemplateID != nil {
			if err := events.EmitTx(ctx, tx, events.Event{
				Type:         entities.EventTypeTemplateApplied,
				OperatorID:   &account.OperatorID,
				AccountID:    &scopedKey.AccountID,
				ResourceType: "template",
				ResourceID:   scopedKey.TemplateID.String(),
				Payload: map[string]any{
					"action":           "create",
					"scoped_key_id":    scopedKey.ID.String(),
					"scoped_key_name":  scopedKey.Name,
					"template_version": *scopedKey.TemplateVersion,
				},
			}); err != nil {
				return fmt.Errorf("emit template.applied_to_scoped_key (create): %w", err)
			}
		}

		result = scopedKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.pushAccountAfterCommit(ctx, result.AccountID)
	return result, nil
}

// GetScopedSigningKey retrieves a scoped signing key by ID
func (s *ScopedSigningKeyService) GetScopedSigningKey(ctx context.Context, id uuid.UUID) (*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().GetByID(ctx, id)
}

// GetScopedSigningKeyByName retrieves a scoped signing key by account ID and name
func (s *ScopedSigningKeyService) GetScopedSigningKeyByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().GetByName(ctx, accountID, name)
}

// GetScopedSigningKeyByPublicKey retrieves a scoped signing key by public key
func (s *ScopedSigningKeyService) GetScopedSigningKeyByPublicKey(ctx context.Context, publicKey string) (*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().GetByPublicKey(ctx, publicKey)
}

// ListScopedSigningKeysByAccount retrieves all scoped signing keys for an account with pagination
func (s *ScopedSigningKeyService) ListScopedSigningKeysByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().ListByAccount(ctx, accountID, opts)
}

// ListAllScopedSigningKeys retrieves all scoped signing keys across all accounts with pagination
func (s *ScopedSigningKeyService) ListAllScopedSigningKeys(ctx context.Context, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().List(ctx, opts)
}

// UpdateScopedSigningKeyRequest contains the fields that can be updated
type UpdateScopedSigningKeyRequest struct {
	Name            *string
	Description     *string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs *int
	ResponseTTL     *time.Duration
}

// UpdateScopedSigningKey updates a scoped signing key's configuration.
// Note: Updating permissions will require regenerating user JWTs that use this key.
func (s *ScopedSigningKeyService) UpdateScopedSigningKey(ctx context.Context, id uuid.UUID, req UpdateScopedSigningKeyRequest) (*entities.ScopedSigningKey, error) {
	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		// Get existing scoped signing key
		scopedKey, err := scopedKeyRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Update fields if provided
		updated := false
		if req.Name != nil && *req.Name != scopedKey.Name {
			// Check if new name is already taken for this account
			existing, err := scopedKeyRepo.GetByName(ctx, scopedKey.AccountID, *req.Name)
			if err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fmt.Errorf("failed to check existing scoped signing key: %w", err)
			}
			if existing != nil && existing.ID != id {
				return repositories.ErrAlreadyExists
			}
			scopedKey.Name = *req.Name
			updated = true
		}

		if req.Description != nil && *req.Description != scopedKey.Description {
			scopedKey.Description = *req.Description
			updated = true
		}

		// Update permission arrays if provided (even if empty). Track
		// separately whether *permission* fields changed — when a
		// templated SSK's permissions are edited directly the row gets
		// flagged as drifted so the UI can show an "edited" badge and
		// the operator knows a future bump would overwrite their changes.
		permissionsTouched := false
		if req.PubAllow != nil {
			scopedKey.PubAllow = req.PubAllow
			updated = true
			permissionsTouched = true
		}
		if req.PubDeny != nil {
			scopedKey.PubDeny = req.PubDeny
			updated = true
			permissionsTouched = true
		}
		if req.SubAllow != nil {
			scopedKey.SubAllow = req.SubAllow
			updated = true
			permissionsTouched = true
		}
		if req.SubDeny != nil {
			scopedKey.SubDeny = req.SubDeny
			updated = true
			permissionsTouched = true
		}

		if req.ResponseMaxMsgs != nil && *req.ResponseMaxMsgs != scopedKey.ResponseMaxMsgs {
			scopedKey.ResponseMaxMsgs = *req.ResponseMaxMsgs
			updated = true
			permissionsTouched = true
		}

		if req.ResponseTTL != nil && *req.ResponseTTL != scopedKey.ResponseTTL {
			scopedKey.ResponseTTL = *req.ResponseTTL
			updated = true
			permissionsTouched = true
		}

		// Reject permission edits on SSKs that are auto-tracking. Without
		// this guard, the next template bump would silently overwrite the
		// operator's edit. Force them to disable tracking (or detach)
		// first so the intent is explicit and auditable.
		if permissionsTouched && scopedKey.TrackLatest {
			return ErrSSKTrackingLatest
		}

		if permissionsTouched && scopedKey.TemplateID != nil && !scopedKey.TemplateDrifted {
			scopedKey.TemplateDrifted = true
		}

		if !updated {
			result = scopedKey
			return nil
		}

		scopedKey.UpdatedAt = clock.Now()

		// Save changes
		if err := scopedKeyRepo.Update(ctx, scopedKey); err != nil {
			return fmt.Errorf("failed to update scoped signing key: %w", err)
		}

		// Re-sign the account JWT so the updated template permissions take
		// effect on NATS. WithTx rolls back the scoped-key update on failure.
		if err := s.regenerateAccountJWTTx(ctx, tx, scopedKey.AccountID); err != nil {
			return err
		}

		account, err := tx.AccountRepository().GetByID(ctx, scopedKey.AccountID)
		if err != nil {
			return fmt.Errorf("emit scoped_key.updated: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyUpdated,
			OperatorID:   &account.OperatorID,
			AccountID:    &scopedKey.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   scopedKey.ID.String(),
			Payload:      map[string]any{"name": scopedKey.Name},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.updated: %w", err)
		}

		result = scopedKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.pushAccountAfterCommit(ctx, result.AccountID)
	return result, nil
}

// DeleteScopedSigningKey deletes a scoped signing key.
// Note: This will cascade to users signed by this key (foreign key constraint).
func (s *ScopedSigningKeyService) DeleteScopedSigningKey(ctx context.Context, id uuid.UUID) error {
	var pushAccountID uuid.UUID
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		// Need the accountID for the post-delete JWT regen.
		existing, err := scopedKeyRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		if err := scopedKeyRepo.Delete(ctx, id); err != nil {
			return err
		}

		// Re-sign the account JWT so NATS stops trusting the deleted key as a signer.
		// On failure, WithTx rolls back the delete itself — the key reappears.
		if err := s.regenerateAccountJWTTx(ctx, tx, existing.AccountID); err != nil {
			return err
		}

		account, err := tx.AccountRepository().GetByID(ctx, existing.AccountID)
		if err != nil {
			return fmt.Errorf("emit scoped_key.deleted: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyDeleted,
			OperatorID:   &account.OperatorID,
			AccountID:    &existing.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   existing.ID.String(),
			Payload:      map[string]any{"name": existing.Name, "public_key": existing.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.deleted: %w", err)
		}

		pushAccountID = existing.AccountID
		return nil
	})
	if err != nil {
		return err
	}
	s.pushAccountAfterCommit(ctx, pushAccountID)
	return nil
}

// BumpScopedKeyTemplate snapshots a target template version's permissions
// into the SSK, sets template_version, clears template_drifted, and
// re-signs the parent account JWT. The post-commit NATS push is wired in
// Task #7; for now the call is DB-only and operators must
// `nisctl cluster sync` to roll out (matches today's SSK Create/Update
// behaviour). When versionNumber == 0, applies the template's current
// latest_version.
func (s *ScopedSigningKeyService) BumpScopedKeyTemplate(ctx context.Context, scopedKeyID uuid.UUID, versionNumber int) (*entities.ScopedSigningKey, error) {
	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		ssk, err := tx.ScopedSigningKeyRepository().GetByID(ctx, scopedKeyID)
		if err != nil {
			return err
		}
		if ssk.TemplateID == nil {
			return fmt.Errorf("%w: %s", ErrScopedKeyNotTemplated, scopedKeyID)
		}
		account, err := tx.AccountRepository().GetByID(ctx, ssk.AccountID)
		if err != nil {
			return fmt.Errorf("get account for bump: %w", err)
		}
		tpl, err := tx.TemplateRepository().GetByID(ctx, *ssk.TemplateID)
		if err != nil {
			return fmt.Errorf("get template: %w", err)
		}
		target := versionNumber
		if target == 0 {
			target = tpl.LatestVersion
		}
		ver, err := tx.TemplateVersionRepository().GetByTemplateAndNumber(ctx, tpl.ID, target)
		if err != nil {
			if errors.Is(err, repositories.ErrNotFound) {
				return fmt.Errorf("%w: template %s version %d", ErrTemplateVersionNotFound, tpl.Name, target)
			}
			return fmt.Errorf("get template version: %w", err)
		}

		ssk.PubAllow = ver.PubAllow
		ssk.PubDeny = ver.PubDeny
		ssk.SubAllow = ver.SubAllow
		ssk.SubDeny = ver.SubDeny
		ssk.ResponseMaxMsgs = ver.ResponseMaxMsgs
		ssk.ResponseTTL = ver.ResponseTTL
		ssk.TemplateVersion = &target
		ssk.TemplateDrifted = false
		ssk.UpdatedAt = clock.Now()

		if err := tx.ScopedSigningKeyRepository().Update(ctx, ssk); err != nil {
			return fmt.Errorf("update SSK on bump: %w", err)
		}
		if err := s.regenerateAccountJWTTx(ctx, tx, ssk.AccountID); err != nil {
			return err
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeTemplateApplied,
			OperatorID:   &account.OperatorID,
			AccountID:    &ssk.AccountID,
			ResourceType: "template",
			ResourceID:   tpl.ID.String(),
			Payload: map[string]any{
				"action":           "bump",
				"scoped_key_id":    ssk.ID.String(),
				"scoped_key_name":  ssk.Name,
				"template_version": target,
			},
		}); err != nil {
			return fmt.Errorf("emit template.applied_to_scoped_key (bump): %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyUpdated,
			OperatorID:   &account.OperatorID,
			AccountID:    &ssk.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   ssk.ID.String(),
			Payload:      map[string]any{"name": ssk.Name, "bumped_to_version": target},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.updated (bump): %w", err)
		}
		result = ssk
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Bump is the textbook auto-sync case: operator explicitly rolled
	// out new permissions; pushing them out immediately is the whole
	// point of P6's templates.
	s.pushAccountAfterCommit(ctx, result.AccountID)
	return result, nil
}

// DetachScopedKeyTemplate clears template_id / template_version /
// template_drifted, leaving the permission columns untouched. The SSK
// becomes a standalone key; future template updates have no effect on
// it. No JWT regen needed — permissions did not change.
func (s *ScopedSigningKeyService) DetachScopedKeyTemplate(ctx context.Context, scopedKeyID uuid.UUID) (*entities.ScopedSigningKey, error) {
	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		ssk, err := tx.ScopedSigningKeyRepository().GetByID(ctx, scopedKeyID)
		if err != nil {
			return err
		}
		if ssk.TemplateID == nil {
			return fmt.Errorf("%w: %s", ErrScopedKeyNotTemplated, scopedKeyID)
		}
		account, err := tx.AccountRepository().GetByID(ctx, ssk.AccountID)
		if err != nil {
			return fmt.Errorf("get account for detach: %w", err)
		}
		priorTemplateID := *ssk.TemplateID
		priorVersion := *ssk.TemplateVersion

		ssk.TemplateID = nil
		ssk.TemplateVersion = nil
		ssk.TemplateDrifted = false
		// Detach implies "no template binding"; tracking-latest without
		// a binding is incoherent, so clear the flag too. The operator
		// can re-enable tracking after re-attaching via create-from-
		// template if they want that behaviour back.
		ssk.TrackLatest = false
		ssk.UpdatedAt = clock.Now()

		if err := tx.ScopedSigningKeyRepository().Update(ctx, ssk); err != nil {
			return fmt.Errorf("update SSK on detach: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeTemplateApplied,
			OperatorID:   &account.OperatorID,
			AccountID:    &ssk.AccountID,
			ResourceType: "template",
			ResourceID:   priorTemplateID.String(),
			Payload: map[string]any{
				"action":              "detach",
				"scoped_key_id":       ssk.ID.String(),
				"scoped_key_name":     ssk.Name,
				"detached_from_version": priorVersion,
			},
		}); err != nil {
			return fmt.Errorf("emit template.applied_to_scoped_key (detach): %w", err)
		}
		result = ssk
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SetScopedKeyTrackLatest toggles the track_latest flag on a templated
// SSK. Enabling requires (a) a template binding and (b) clean state
// (TemplateDrifted == false) — turning tracking on while drifted would
// silently overwrite the operator's drift on the next template bump,
// the exact footgun the rejection-on-edit guard exists to prevent.
// Disabling is unconditional. Neither path regenerates the account
// JWT — permission columns aren't changed.
func (s *ScopedSigningKeyService) SetScopedKeyTrackLatest(ctx context.Context, scopedKeyID uuid.UUID, enabled bool) (*entities.ScopedSigningKey, error) {
	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		ssk, err := tx.ScopedSigningKeyRepository().GetByID(ctx, scopedKeyID)
		if err != nil {
			return err
		}
		if enabled {
			if ssk.TemplateID == nil {
				return fmt.Errorf("%w: %s", ErrSSKTrackLatestRequiresTemplate, scopedKeyID)
			}
			if ssk.TemplateDrifted {
				return fmt.Errorf("%w: %s", ErrSSKTrackLatestDrifted, scopedKeyID)
			}
		}
		if ssk.TrackLatest == enabled {
			result = ssk
			return nil
		}
		ssk.TrackLatest = enabled
		ssk.UpdatedAt = clock.Now()
		if err := tx.ScopedSigningKeyRepository().Update(ctx, ssk); err != nil {
			return fmt.Errorf("update SSK on set-track-latest: %w", err)
		}
		account, err := tx.AccountRepository().GetByID(ctx, ssk.AccountID)
		if err != nil {
			return fmt.Errorf("set-track-latest: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyUpdated,
			OperatorID:   &account.OperatorID,
			AccountID:    &ssk.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   ssk.ID.String(),
			Payload: map[string]any{
				"name":         ssk.Name,
				"track_latest": enabled,
			},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.updated (set-track-latest): %w", err)
		}
		result = ssk
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
