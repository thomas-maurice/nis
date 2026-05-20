package services

import (
	"context"
	"errors"
	"fmt"

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

// AccountService provides business logic for account management.
//
// Multi-write methods (CreateAccount, UpdateAccount, UpdateJetStreamLimits)
// run inside a single repository-level transaction via factory.WithTx, so
// partial failures don't leave half-created accounts or accounts whose JWT
// references a scoped key that wasn't actually persisted.
type AccountService struct {
	factory        persistence.RepositoryFactory
	jwtService     *JWTService
	encryptor      encryption.Encryptor
	clusterService *ClusterService // optional; set via WithClusterService for NATS-side cleanup on delete
}

// NewAccountService creates a new account service.
func NewAccountService(
	factory persistence.RepositoryFactory,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *AccountService {
	return &AccountService{
		factory:    factory,
		jwtService: jwtService,
		encryptor:  encryptor,
	}
}

// WithClusterService attaches a ClusterService used to propagate account
// JWT changes (create / update / delete / jetstream-limit) to the NATS
// resolver(s) attached to the operator. Call this from serve.go AFTER
// both services exist (the two are mutually independent at construction,
// so no init-order surprise). Tests that don't wire this skip the
// NATS-side push — the DB-side mutation and audit event still happen,
// and `nisctl cluster sync` reconciles when the operator is ready.
func (s *AccountService) WithClusterService(cs *ClusterService) *AccountService {
	s.clusterService = cs
	return s
}

// pushAccountAfterCommit pushes the (already-committed) account JWT to
// every cluster attached to its operator. Best-effort: per-cluster
// failures are logged and the call returns nil. The DB is the source of
// truth; the drift dashboard (P9) surfaces any cluster that fell behind.
//
// This is the auto-sync side of P6: before P6, mutating an account or
// SKK left the DB ahead of NATS until an operator ran `nisctl cluster
// sync`. Now every mutation that changes the account JWT triggers a
// push as part of the same operation, with the same best-effort
// semantics DeleteAccount uses.
func (s *AccountService) pushAccountAfterCommit(ctx context.Context, account *entities.Account) {
	if s.clusterService == nil || account == nil || account.JWT == "" {
		return
	}
	errs := s.clusterService.PushAccountToAllClusters(ctx, account.OperatorID, account)
	if len(errs) == 0 {
		return
	}
	log := logging.LogFromContext(ctx)
	for _, e := range errs {
		log.Warn("account JWT push failed; run 'nisctl cluster sync' to reconcile",
			"account", account.Name,
			"account_public_key", account.PublicKey,
			"error", e.Error,
		)
	}
}

// CreateAccountRequest contains the data needed to create an account
type CreateAccountRequest struct {
	OperatorID            uuid.UUID
	Name                  string
	Description           string
	JetStreamEnabled      bool
	JetStreamMaxMemory    int64
	JetStreamMaxStorage   int64
	JetStreamMaxStreams   int64
	JetStreamMaxConsumers int64
}

// CreateAccount creates a new account with generated keys and JWT. It opens a
// transaction; nested service calls (e.g. OperatorService creating $SYS) must
// use createAccountTx and pass the surrounding tx-scoped factory instead.
func (s *AccountService) CreateAccount(ctx context.Context, req CreateAccountRequest) (*entities.Account, error) {
	var account *entities.Account
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		a, e := s.createAccountTx(ctx, tx, req)
		account = a
		return e
	})
	if err != nil {
		return nil, err
	}
	s.pushAccountAfterCommit(ctx, account)
	return account, nil
}

// createAccountTx is the transactional body of CreateAccount. The caller is
// responsible for the surrounding factory.WithTx; calling this with a non-tx
// factory still works but loses the atomic-rollback guarantee.
func (s *AccountService) createAccountTx(ctx context.Context, tx persistence.RepositoryFactory, req CreateAccountRequest) (*entities.Account, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("account name is required")
	}

	accountRepo := tx.AccountRepository()
	operatorRepo := tx.OperatorRepository()
	scopedKeyRepo := tx.ScopedSigningKeyRepository()

	// Get operator to sign the account JWT
	operator, err := operatorRepo.GetByID(ctx, req.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Check if account with this name already exists for this operator
	existing, err := accountRepo.GetByName(ctx, req.OperatorID, req.Name)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, fmt.Errorf("failed to check existing account: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// Generate account NKey pair
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account keys: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt account seed: %w", err)
	}

	accountID := uuid.New()

	// Build the default scoped signing key in memory first so we can declare it
	// inside the account JWT's `signing_keys` claim before any of it goes to disk.
	// NATS requires the scoped signing key to appear in the account JWT, otherwise
	// users signed by that key are rejected with "Authorization Violation."
	defaultKey, err := s.buildDefaultScopedSigningKey(ctx, accountID)
	if err != nil {
		return nil, err
	}

	// Create account entity
	account := &entities.Account{
		ID:                    accountID,
		OperatorID:            req.OperatorID,
		Name:                  req.Name,
		Description:           req.Description,
		EncryptedSeed:         encryptedSeed,
		PublicKey:             pubKey,
		JetStreamEnabled:      req.JetStreamEnabled,
		JetStreamMaxMemory:    req.JetStreamMaxMemory,
		JetStreamMaxStorage:   req.JetStreamMaxStorage,
		JetStreamMaxStreams:   req.JetStreamMaxStreams,
		JetStreamMaxConsumers: req.JetStreamMaxConsumers,
		CreatedAt:             clock.Now(),
		UpdatedAt:             clock.Now(),
	}

	// Generate JWT signed by operator, declaring the default scoped key as a signer.
	// New account has no revocations yet; account TTL flows from operator policy.
	jwt, err := s.jwtService.GenerateAccountJWT(ctx, account, operator, []*entities.ScopedSigningKey{defaultKey}, nil, operator.AccountJWTTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account JWT: %w", err)
	}
	account.JWT = jwt

	// Save account first so the scoped key's FK to accounts(id) is satisfied.
	if err := accountRepo.Create(ctx, account); err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}

	if err := scopedKeyRepo.Create(ctx, defaultKey); err != nil {
		// Tx rollback (handled by WithTx in the caller) reverts the account
		// row. No manual cleanup needed — that's the whole point of A1.
		return nil, fmt.Errorf("failed to create default scoped signing key for account: %w", err)
	}

	if err := events.EmitTx(ctx, tx, events.Event{
		Type:         entities.EventTypeScopedKeyCreated,
		OperatorID:   &account.OperatorID,
		AccountID:    &account.ID,
		ResourceType: "scoped_key",
		ResourceID:   defaultKey.ID.String(),
		Payload:      map[string]any{"name": defaultKey.Name, "public_key": defaultKey.PublicKey},
	}); err != nil {
		return nil, fmt.Errorf("emit scoped_key.created (default): %w", err)
	}

	logging.LogFromContext(ctx).Info("created account with default scoped signing key",
		"account", account.Name, "scoped_key", defaultKey.Name)

	if err := events.EmitTx(ctx, tx, events.Event{
		Type:         entities.EventTypeAccountCreated,
		OperatorID:   &account.OperatorID,
		AccountID:    &account.ID,
		ResourceType: "account",
		ResourceID:   account.ID.String(),
		Payload:      map[string]any{"name": account.Name, "public_key": account.PublicKey},
	}); err != nil {
		return nil, fmt.Errorf("emit account.created: %w", err)
	}

	return account, nil
}

// buildDefaultScopedSigningKey constructs (but does NOT persist) the default scoped
// signing key for a new account. Returns the in-memory entity so the caller can
// embed it in the account JWT before saving anything.
func (s *AccountService) buildDefaultScopedSigningKey(ctx context.Context, accountID uuid.UUID) (*entities.ScopedSigningKey, error) {
	// Generate account NKey pair (scoped signing keys use account key prefix)
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to generate scoped signing key: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt scoped signing key seed: %w", err)
	}

	return &entities.ScopedSigningKey{
		ID:              uuid.New(),
		AccountID:       accountID,
		Name:            "default",
		Description:     "Default scoped signing key with unlimited account permissions",
		EncryptedSeed:   encryptedSeed,
		PublicKey:       pubKey,
		PubAllow:        []string{}, // Empty = allow all
		PubDeny:         []string{},
		SubAllow:        []string{}, // Empty = allow all
		SubDeny:         []string{},
		ResponseMaxMsgs: 0, // 0 = unlimited
		ResponseTTL:     0, // 0 = unlimited
		CreatedAt:       clock.Now(),
		UpdatedAt:       clock.Now(),
	}, nil
}

// GetAccount retrieves an account by ID
func (s *AccountService) GetAccount(ctx context.Context, id uuid.UUID) (*entities.Account, error) {
	return s.factory.AccountRepository().GetByID(ctx, id)
}

// GetAccountByName retrieves an account by operator ID and name
func (s *AccountService) GetAccountByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Account, error) {
	return s.factory.AccountRepository().GetByName(ctx, operatorID, name)
}

// GetAccountByPublicKey retrieves an account by public key
func (s *AccountService) GetAccountByPublicKey(ctx context.Context, publicKey string) (*entities.Account, error) {
	return s.factory.AccountRepository().GetByPublicKey(ctx, publicKey)
}

// ListAccountsByOperator retrieves all accounts for an operator with pagination
func (s *AccountService) ListAccountsByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Account, error) {
	return s.factory.AccountRepository().ListByOperator(ctx, operatorID, opts)
}

// ListAllAccounts lists all accounts across all operators
func (s *AccountService) ListAllAccounts(ctx context.Context, opts repositories.ListOptions) ([]*entities.Account, error) {
	return s.factory.AccountRepository().List(ctx, opts)
}

// UpdateAccountRequest contains the fields that can be updated
type UpdateAccountRequest struct {
	Name        *string
	Description *string
}

// UpdateAccount updates an account's metadata and regenerates JWT
func (s *AccountService) UpdateAccount(ctx context.Context, id uuid.UUID, req UpdateAccountRequest) (*entities.Account, error) {
	var account *entities.Account
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		accountRepo := tx.AccountRepository()
		operatorRepo := tx.OperatorRepository()
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		// Get existing account
		acc, err := accountRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Update fields if provided
		updated := false
		if req.Name != nil && *req.Name != acc.Name {
			// Check if new name is already taken for this operator
			existing, err := accountRepo.GetByName(ctx, acc.OperatorID, *req.Name)
			if err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fmt.Errorf("failed to check existing account: %w", err)
			}
			if existing != nil && existing.ID != id {
				return repositories.ErrAlreadyExists
			}
			acc.Name = *req.Name
			updated = true
		}

		if req.Description != nil && *req.Description != acc.Description {
			acc.Description = *req.Description
			updated = true
		}

		if !updated {
			account = acc
			return nil
		}

		acc.UpdatedAt = clock.Now()

		// Get operator to sign the updated JWT
		operator, err := operatorRepo.GetByID(ctx, acc.OperatorID)
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		// Fetch existing scoped signing keys so the regenerated JWT continues to declare
		// them as authorised signers. Skipping this would invalidate every user signed
		// by a scoped key as soon as the account is updated.
		scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, acc.ID, repositories.ListOptions{Limit: 1000})
		if err != nil {
			return fmt.Errorf("failed to list scoped signing keys: %w", err)
		}

		// Fetch active revocations so the regenerated JWT keeps NATS rejecting
		// previously-revoked user JWTs.
		revs, err := tx.UserJWTRevocationRepository().ListActiveByAccount(ctx, acc.ID)
		if err != nil {
			return fmt.Errorf("failed to list active revocations: %w", err)
		}

		// Regenerate JWT with updated metadata
		jwt, err := s.jwtService.GenerateAccountJWT(ctx, acc, operator, scopedKeys, revs, operator.AccountJWTTTL)
		if err != nil {
			return fmt.Errorf("failed to regenerate account JWT: %w", err)
		}
		acc.JWT = jwt

		// Save changes
		if err := accountRepo.Update(ctx, acc); err != nil {
			return fmt.Errorf("failed to update account: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAccountUpdated,
			OperatorID:   &acc.OperatorID,
			AccountID:    &acc.ID,
			ResourceType: "account",
			ResourceID:   acc.ID.String(),
			Payload:      map[string]any{"name": acc.Name},
		}); err != nil {
			return fmt.Errorf("emit account.updated: %w", err)
		}

		account = acc
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.pushAccountAfterCommit(ctx, account)
	return account, nil
}

// UpdateJetStreamLimitsRequest contains JetStream configuration
type UpdateJetStreamLimitsRequest struct {
	Enabled      bool
	MaxMemory    int64
	MaxStorage   int64
	MaxStreams   int64
	MaxConsumers int64
}

// UpdateJetStreamLimits updates JetStream limits and regenerates JWT
func (s *AccountService) UpdateJetStreamLimits(ctx context.Context, id uuid.UUID, req UpdateJetStreamLimitsRequest) (*entities.Account, error) {
	var account *entities.Account
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		accountRepo := tx.AccountRepository()
		operatorRepo := tx.OperatorRepository()
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		acc, err := accountRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Update JetStream configuration
		acc.JetStreamEnabled = req.Enabled
		acc.JetStreamMaxMemory = req.MaxMemory
		acc.JetStreamMaxStorage = req.MaxStorage
		acc.JetStreamMaxStreams = req.MaxStreams
		acc.JetStreamMaxConsumers = req.MaxConsumers
		acc.UpdatedAt = clock.Now()

		// Get operator to sign the updated JWT
		operator, err := operatorRepo.GetByID(ctx, acc.OperatorID)
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		// Fetch existing scoped signing keys so the regenerated JWT continues to declare
		// them as authorised signers (see UpdateAccount).
		scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, acc.ID, repositories.ListOptions{Limit: 1000})
		if err != nil {
			return fmt.Errorf("failed to list scoped signing keys: %w", err)
		}

		// Same revocation passthrough as UpdateAccount.
		revs, err := tx.UserJWTRevocationRepository().ListActiveByAccount(ctx, acc.ID)
		if err != nil {
			return fmt.Errorf("failed to list active revocations: %w", err)
		}

		// Regenerate JWT with new JetStream limits
		jwt, err := s.jwtService.GenerateAccountJWT(ctx, acc, operator, scopedKeys, revs, operator.AccountJWTTTL)
		if err != nil {
			return fmt.Errorf("failed to regenerate account JWT: %w", err)
		}
		acc.JWT = jwt

		// Save changes
		if err := accountRepo.Update(ctx, acc); err != nil {
			return fmt.Errorf("failed to update account: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAccountUpdated,
			OperatorID:   &acc.OperatorID,
			AccountID:    &acc.ID,
			ResourceType: "account",
			ResourceID:   acc.ID.String(),
			Payload:      map[string]any{"name": acc.Name, "changed": []string{"jetstream_limits"}},
		}); err != nil {
			return fmt.Errorf("emit account.updated: %w", err)
		}

		account = acc
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.pushAccountAfterCommit(ctx, account)
	return account, nil
}

// DeleteAccount deletes an account and all associated data (cascades to users).
// After the DB transaction commits successfully, sends an operator-signed
// $SYS.REQ.CLAIMS.DELETE to every cluster attached to the operator so the
// account's JWT no longer sits on the resolver — without that step, any
// .creds previously issued under the account would keep connecting to NATS
// indefinitely (effectively forever under the default no-expiry policy).
//
// Per-cluster delete failures are logged but do NOT roll back the DB delete.
// Mirrors A6's "NATS is reconciled best-effort, DB is the source of truth"
// rule and the existing PushAccountToAllClusters semantic — a transient
// resolver hiccup must not block an operator from removing an account.
func (s *AccountService) DeleteAccount(ctx context.Context, id uuid.UUID) error {
	var (
		operatorID       uuid.UUID
		accountPublicKey string
		accountName      string
	)

	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		accountRepo := tx.AccountRepository()
		operatorRepo := tx.OperatorRepository()

		// Check if account exists
		account, err := accountRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Check if this account is a system account for any operator
		operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		if operator.SystemAccountPubKey == account.PublicKey {
			return fmt.Errorf("cannot delete system account: this account is designated as the system account for operator '%s'", operator.Name)
		}

		// Delete account (cascades to users and scoped signing keys at FK level)
		if err := accountRepo.Delete(ctx, id); err != nil {
			return err
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAccountDeleted,
			OperatorID:   &account.OperatorID,
			AccountID:    &account.ID,
			ResourceType: "account",
			ResourceID:   account.ID.String(),
			Payload:      map[string]any{"name": account.Name, "public_key": account.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit account.deleted: %w", err)
		}

		operatorID = account.OperatorID
		accountPublicKey = account.PublicKey
		accountName = account.Name
		return nil
	})
	if err != nil {
		return err
	}

	// DB commit succeeded — reconcile the resolver. clusterService is nil in
	// tests that don't wire it; tolerate that without skipping the audit
	// trail above.
	if s.clusterService != nil && accountPublicKey != "" {
		if delErrs := s.clusterService.DeleteAccountFromAllClusters(ctx, operatorID, accountPublicKey); len(delErrs) > 0 {
			log := logging.LogFromContext(ctx)
			for _, e := range delErrs {
				log.Warn("account.deleted: resolver delete failed; run 'nisctl cluster sync --prune' to reconcile",
					"account", accountName,
					"account_public_key", accountPublicKey,
					"error", e.Error,
				)
			}
		}
	}

	return nil
}

// AccountJWTRevocationView denormalises a UserJWTRevocation with the user's
// current Name and whether the user row is still flagged as revoked. The
// "still flagged" bit diverges from the revocation row's mere existence after
// RegenerateUserCredentials clears users.revoked_at — the revocation row stays
// active in the parent account JWT until its JWTExp regardless.
type AccountJWTRevocationView struct {
	Revocation       *entities.UserJWTRevocation
	UserName         string
	UserStillFlagged bool
}

// ListJWTRevocations returns the active (PrunedAt IS NULL) user-JWT revocations
// for the account, joined with the current user row for display purposes.
// Read-only — no transaction needed.
func (s *AccountService) ListJWTRevocations(ctx context.Context, accountID uuid.UUID) ([]*AccountJWTRevocationView, error) {
	revRepo := s.factory.UserJWTRevocationRepository()
	userRepo := s.factory.UserRepository()

	revs, err := revRepo.ListActiveByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list active revocations: %w", err)
	}

	views := make([]*AccountJWTRevocationView, 0, len(revs))
	for _, rev := range revs {
		view := &AccountJWTRevocationView{Revocation: rev}
		if rev.UserID != nil {
			user, err := userRepo.GetByID(ctx, *rev.UserID)
			switch {
			case err == nil:
				view.UserName = user.Name
				view.UserStillFlagged = user.RevokedAt != nil
			case errors.Is(err, repositories.ErrNotFound):
				// User hard-deleted after revocation; revocation outlives it
				// by design. Leave UserName empty, UserStillFlagged false.
			default:
				return nil, fmt.Errorf("get user %s for revocation %s: %w", rev.UserID, rev.ID, err)
			}
		}
		views = append(views, view)
	}
	return views, nil
}
