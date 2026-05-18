package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ExportFormat enumerates the on-disk encodings supported by Export/Import.
// Empty string is treated as FormatJSON for backward compatibility with
// pre-yaml callers.
type ExportFormat string

const (
	FormatJSON ExportFormat = "json"
	FormatYAML ExportFormat = "yaml"
)

// SecretsMode controls the form in which NKey seeds are emitted in an export.
// Every export carries seed material — a "metadata-only" mode is intentionally
// not supported because it produces an unrecoverable half-restore (see
// proto/nis/v1/export.proto).
//
//   - SecretsEncrypted: storage refs as-is (e.g. "encrypted:keyid:..."). The
//     destination must be configured with the same encryption key to decrypt.
//   - SecretsPlaintext: seeds decrypted with the source's encryption key and
//     written as plaintext NKey seeds (e.g. "SO..."). DANGEROUS — the export
//     becomes a plaintext key vault. Use only for disaster-recovery backups
//     that must remain readable across key loss/rotation. Imports of a
//     plaintext export re-encrypt every seed with the destination's current
//     encryption key.
type SecretsMode string

const (
	SecretsEncrypted SecretsMode = "encrypted"
	SecretsPlaintext SecretsMode = "plaintext"
)

// ErrOperatorImportExists is returned by ImportOperator when the export's
// operator ID already exists in the destination and overwrite was not set.
// Handlers should surface this as a precondition failure — the caller has to
// opt in to subtree replacement.
var ErrOperatorImportExists = errors.New("operator with that ID already exists")

// ErrOperatorImportNameConflict is returned when the import's operator name
// is already taken by a *different* operator ID. Overwrite cannot resolve this
// — the caller has to rename or delete the conflicting operator manually.
var ErrOperatorImportNameConflict = errors.New("operator name already taken by a different ID")

// ExportService provides business logic for exporting and importing operators
type ExportService struct {
	factory          persistence.RepositoryFactory
	operatorRepo     repositories.OperatorRepository
	accountRepo      repositories.AccountRepository
	userRepo         repositories.UserRepository
	scopedKeyRepo    repositories.ScopedSigningKeyRepository
	clusterRepo      repositories.ClusterRepository
	operatorService  *OperatorService
	accountService   *AccountService
	userService      *UserService
	scopedKeyService *ScopedSigningKeyService
	clusterService   *ClusterService
	encryptor        encryption.Encryptor
}

// NewExportService creates a new export service. The factory enables tx-wrapped
// multi-write paths (notably ImportFromNSC) that need to roll back the dozens
// of writes they make if any step fails. Individual repos and the inner
// services are still injected for the JSON-import path and for read-only
// queries; over time the factory should subsume them.
func NewExportService(
	factory persistence.RepositoryFactory,
	operatorRepo repositories.OperatorRepository,
	accountRepo repositories.AccountRepository,
	userRepo repositories.UserRepository,
	scopedKeyRepo repositories.ScopedSigningKeyRepository,
	clusterRepo repositories.ClusterRepository,
	operatorService *OperatorService,
	accountService *AccountService,
	userService *UserService,
	scopedKeyService *ScopedSigningKeyService,
	clusterService *ClusterService,
	encryptor encryption.Encryptor,
) *ExportService {
	return &ExportService{
		factory:          factory,
		operatorRepo:     operatorRepo,
		accountRepo:      accountRepo,
		userRepo:         userRepo,
		scopedKeyRepo:    scopedKeyRepo,
		clusterRepo:      clusterRepo,
		operatorService:  operatorService,
		accountService:   accountService,
		userService:      userService,
		scopedKeyService: scopedKeyService,
		clusterService:   clusterService,
		encryptor:        encryptor,
	}
}

// ExportedOperator represents a complete export of an operator and all its data.
// Both json and yaml struct tags are populated so encoders for either format
// produce the same field names (yaml.v3 defaults to lowercased Go field names
// otherwise, which would diverge from JSON).
type ExportedOperator struct {
	Version    string                   `json:"version" yaml:"version"`
	ExportedAt time.Time                `json:"exported_at" yaml:"exported_at"`
	Operator   *ExportedOperatorData    `json:"operator" yaml:"operator"`
	Accounts   []*ExportedAccountData   `json:"accounts" yaml:"accounts"`
	ScopedKeys []*ExportedScopedKeyData `json:"scoped_keys" yaml:"scoped_keys"`
	Users      []*ExportedUserData      `json:"users" yaml:"users"`
	Clusters   []*ExportedClusterData   `json:"clusters,omitempty" yaml:"clusters,omitempty"`
}

// Secret material is carried in mutually-exclusive sibling fields across every
// Exported*Data struct:
//
//   - `encrypted_seed` (or `encrypted_creds`): storage refs from the source's
//     encryptor. Importable only by a server that holds the same encryption
//     key.
//   - `seed` (or `creds`): plaintext NKey seeds (or .creds bytes) — present
//     only when the export was created in `plaintext` mode. The importer
//     re-encrypts these with its own current key.
//
// Exactly one of the pair is populated per row, depending on SecretsMode at
// export time. Both use `omitempty` so a SecretsNone export contains neither
// and stays minimal.

// ExportedOperatorData contains operator data including encrypted seed
type ExportedOperatorData struct {
	ID                  uuid.UUID `json:"id" yaml:"id"`
	Name                string    `json:"name" yaml:"name"`
	Description         string    `json:"description" yaml:"description"`
	PublicKey           string    `json:"public_key" yaml:"public_key"`
	EncryptedSeed       string    `json:"encrypted_seed,omitempty" yaml:"encrypted_seed,omitempty"`
	Seed                string    `json:"seed,omitempty" yaml:"seed,omitempty"`
	SystemAccountPubKey string    `json:"system_account_pub_key" yaml:"system_account_pub_key"`
	JWT                 string    `json:"jwt" yaml:"jwt"`
	CreatedAt           time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt           time.Time `json:"updated_at" yaml:"updated_at"`
}

// ExportedAccountData contains account data
type ExportedAccountData struct {
	ID                    uuid.UUID `json:"id" yaml:"id"`
	OperatorID            uuid.UUID `json:"operator_id" yaml:"operator_id"`
	Name                  string    `json:"name" yaml:"name"`
	Description           string    `json:"description" yaml:"description"`
	PublicKey             string    `json:"public_key" yaml:"public_key"`
	EncryptedSeed         string    `json:"encrypted_seed,omitempty" yaml:"encrypted_seed,omitempty"`
	Seed                  string    `json:"seed,omitempty" yaml:"seed,omitempty"`
	JetStreamEnabled      bool      `json:"jetstream_enabled" yaml:"jetstream_enabled"`
	JetStreamMaxMemory    int64     `json:"jetstream_max_memory" yaml:"jetstream_max_memory"`
	JetStreamMaxStorage   int64     `json:"jetstream_max_storage" yaml:"jetstream_max_storage"`
	JetStreamMaxStreams   int64     `json:"jetstream_max_streams" yaml:"jetstream_max_streams"`
	JetStreamMaxConsumers int64     `json:"jetstream_max_consumers" yaml:"jetstream_max_consumers"`
	JWT                   string    `json:"jwt" yaml:"jwt"`
	CreatedAt             time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt             time.Time `json:"updated_at" yaml:"updated_at"`
}

// ExportedScopedKeyData contains scoped signing key data
type ExportedScopedKeyData struct {
	ID              uuid.UUID     `json:"id" yaml:"id"`
	AccountID       uuid.UUID     `json:"account_id" yaml:"account_id"`
	Name            string        `json:"name" yaml:"name"`
	Description     string        `json:"description" yaml:"description"`
	PublicKey       string        `json:"public_key" yaml:"public_key"`
	EncryptedSeed   string        `json:"encrypted_seed,omitempty" yaml:"encrypted_seed,omitempty"`
	Seed            string        `json:"seed,omitempty" yaml:"seed,omitempty"`
	PubAllow        []string      `json:"pub_allow" yaml:"pub_allow"`
	PubDeny         []string      `json:"pub_deny" yaml:"pub_deny"`
	SubAllow        []string      `json:"sub_allow" yaml:"sub_allow"`
	SubDeny         []string      `json:"sub_deny" yaml:"sub_deny"`
	ResponseMaxMsgs int           `json:"response_max_msgs" yaml:"response_max_msgs"`
	ResponseTTL     time.Duration `json:"response_ttl" yaml:"response_ttl"`
	CreatedAt       time.Time     `json:"created_at" yaml:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at" yaml:"updated_at"`
}

// ExportedUserData contains user data
type ExportedUserData struct {
	ID                 uuid.UUID  `json:"id" yaml:"id"`
	AccountID          uuid.UUID  `json:"account_id" yaml:"account_id"`
	Name               string     `json:"name" yaml:"name"`
	Description        string     `json:"description" yaml:"description"`
	PublicKey          string     `json:"public_key" yaml:"public_key"`
	EncryptedSeed      string     `json:"encrypted_seed,omitempty" yaml:"encrypted_seed,omitempty"`
	Seed               string     `json:"seed,omitempty" yaml:"seed,omitempty"`
	JWT                string     `json:"jwt" yaml:"jwt"`
	ScopedSigningKeyID *uuid.UUID `json:"scoped_signing_key_id,omitempty" yaml:"scoped_signing_key_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" yaml:"updated_at"`
}

// ExportedClusterData contains cluster data
type ExportedClusterData struct {
	ID                  uuid.UUID `json:"id" yaml:"id"`
	OperatorID          uuid.UUID `json:"operator_id" yaml:"operator_id"`
	Name                string    `json:"name" yaml:"name"`
	Description         string    `json:"description" yaml:"description"`
	ServerURLs          []string  `json:"server_urls" yaml:"server_urls"`
	SystemAccountPubKey string    `json:"system_account_pub_key" yaml:"system_account_pub_key"`
	EncryptedCreds      string    `json:"encrypted_creds,omitempty" yaml:"encrypted_creds,omitempty"`
	Creds               string    `json:"creds,omitempty" yaml:"creds,omitempty"`
	CreatedAt           time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt           time.Time `json:"updated_at" yaml:"updated_at"`
}

// ExportOperator exports an operator and all its associated data.
//
// `mode` controls which (if any) secret material is emitted. See SecretsMode.
// SecretsPlaintext decrypts every seed/creds blob with the source encryptor;
// any failure aborts the export (we won't ship a half-encrypted, half-plaintext
// archive).
func (s *ExportService) ExportOperator(ctx context.Context, operatorID uuid.UUID, mode SecretsMode) (*ExportedOperator, error) {
	switch mode {
	case "":
		mode = SecretsEncrypted
	case SecretsEncrypted, SecretsPlaintext:
		// valid
	default:
		return nil, fmt.Errorf("invalid secrets mode: %q", mode)
	}
	if mode == SecretsPlaintext && s.encryptor == nil {
		return nil, fmt.Errorf("cannot export with plaintext secrets: no encryptor configured")
	}

	// Get operator
	operator, err := s.operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	exported := &ExportedOperator{
		Version:    "1.0",
		ExportedAt: clock.Now(),
		Operator: &ExportedOperatorData{
			ID:                  operator.ID,
			Name:                operator.Name,
			Description:         operator.Description,
			PublicKey:           operator.PublicKey,
			SystemAccountPubKey: operator.SystemAccountPubKey,
			JWT:                 operator.JWT,
			CreatedAt:           operator.CreatedAt,
			UpdatedAt:           operator.UpdatedAt,
		},
		Accounts:   make([]*ExportedAccountData, 0),
		ScopedKeys: make([]*ExportedScopedKeyData, 0),
		Users:      make([]*ExportedUserData, 0),
		Clusters:   make([]*ExportedClusterData, 0),
	}

	if err := s.fillSeed(ctx, mode, operator.EncryptedSeed, &exported.Operator.EncryptedSeed, &exported.Operator.Seed, "operator "+operator.Name); err != nil {
		return nil, err
	}

	// Get all accounts for this operator
	accounts, err := s.accountRepo.ListByOperator(ctx, operatorID, repositories.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	for _, account := range accounts {
		exportedAccount := &ExportedAccountData{
			ID:                    account.ID,
			OperatorID:            account.OperatorID,
			Name:                  account.Name,
			Description:           account.Description,
			PublicKey:             account.PublicKey,
			JetStreamEnabled:      account.JetStreamEnabled,
			JetStreamMaxMemory:    account.JetStreamMaxMemory,
			JetStreamMaxStorage:   account.JetStreamMaxStorage,
			JetStreamMaxStreams:   account.JetStreamMaxStreams,
			JetStreamMaxConsumers: account.JetStreamMaxConsumers,
			JWT:                   account.JWT,
			CreatedAt:             account.CreatedAt,
			UpdatedAt:             account.UpdatedAt,
		}

		if err := s.fillSeed(ctx, mode, account.EncryptedSeed, &exportedAccount.EncryptedSeed, &exportedAccount.Seed, "account "+account.Name); err != nil {
			return nil, err
		}

		exported.Accounts = append(exported.Accounts, exportedAccount)

		// Get scoped signing keys for this account
		scopedKeys, err := s.scopedKeyRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list scoped keys for account %s: %w", account.ID, err)
		}

		for _, key := range scopedKeys {
			exportedKey := &ExportedScopedKeyData{
				ID:              key.ID,
				AccountID:       key.AccountID,
				Name:            key.Name,
				Description:     key.Description,
				PublicKey:       key.PublicKey,
				PubAllow:        key.PubAllow,
				PubDeny:         key.PubDeny,
				SubAllow:        key.SubAllow,
				SubDeny:         key.SubDeny,
				ResponseMaxMsgs: key.ResponseMaxMsgs,
				ResponseTTL:     key.ResponseTTL,
				CreatedAt:       key.CreatedAt,
				UpdatedAt:       key.UpdatedAt,
			}

			if err := s.fillSeed(ctx, mode, key.EncryptedSeed, &exportedKey.EncryptedSeed, &exportedKey.Seed, "scoped key "+key.Name); err != nil {
				return nil, err
			}

			exported.ScopedKeys = append(exported.ScopedKeys, exportedKey)
		}

		// Get users for this account
		users, err := s.userRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list users for account %s: %w", account.ID, err)
		}

		for _, user := range users {
			exportedUser := &ExportedUserData{
				ID:                 user.ID,
				AccountID:          user.AccountID,
				Name:               user.Name,
				Description:        user.Description,
				PublicKey:          user.PublicKey,
				JWT:                user.JWT,
				ScopedSigningKeyID: user.ScopedSigningKeyID,
				CreatedAt:          user.CreatedAt,
				UpdatedAt:          user.UpdatedAt,
			}

			if err := s.fillSeed(ctx, mode, user.EncryptedSeed, &exportedUser.EncryptedSeed, &exportedUser.Seed, "user "+user.Name); err != nil {
				return nil, err
			}

			exported.Users = append(exported.Users, exportedUser)
		}
	}

	// Get clusters for this operator
	clusters, err := s.clusterRepo.ListByOperator(ctx, operatorID, repositories.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	for _, cluster := range clusters {
		exportedCluster := &ExportedClusterData{
			ID:                  cluster.ID,
			OperatorID:          cluster.OperatorID,
			Name:                cluster.Name,
			Description:         cluster.Description,
			ServerURLs:          cluster.ServerURLs,
			SystemAccountPubKey: cluster.SystemAccountPubKey,
			CreatedAt:           cluster.CreatedAt,
			UpdatedAt:           cluster.UpdatedAt,
		}

		if err := s.fillSeed(ctx, mode, cluster.EncryptedCreds, &exportedCluster.EncryptedCreds, &exportedCluster.Creds, "cluster "+cluster.Name); err != nil {
			return nil, err
		}

		exported.Clusters = append(exported.Clusters, exportedCluster)
	}

	return exported, nil
}

// fillSeed writes the source's encrypted blob (stored as a storage ref) into
// either `encryptedDst` (SecretsEncrypted: copy as-is) or `plaintextDst`
// (SecretsPlaintext: decrypt, write plaintext bytes as a string). An empty
// source blob is left empty in either mode — some entities legitimately have
// no seed material yet (e.g. a freshly-registered cluster row with no system
// creds).
func (s *ExportService) fillSeed(ctx context.Context, mode SecretsMode, source string, encryptedDst, plaintextDst *string, label string) error {
	if source == "" {
		return nil
	}
	switch mode {
	case SecretsEncrypted:
		*encryptedDst = source
		return nil
	case SecretsPlaintext:
		plain, err := s.encryptor.Decrypt(ctx, source)
		if err != nil {
			return fmt.Errorf("failed to decrypt %s for plaintext export: %w", label, err)
		}
		*plaintextDst = string(plain)
		return nil
	default:
		return fmt.Errorf("invalid secrets mode: %q", mode)
	}
}

// ExportOperatorJSON exports an operator to JSON
func (s *ExportService) ExportOperatorJSON(ctx context.Context, operatorID uuid.UUID, mode SecretsMode) ([]byte, error) {
	exported, err := s.ExportOperator(ctx, operatorID, mode)
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal export: %w", err)
	}

	return data, nil
}

// ImportOperator imports an operator and its entire subtree as a faithful
// restore. UUIDs, NKey public keys, JWTs, and timestamps are preserved
// byte-for-byte from the export — the only field touched is UpdatedAt, which
// reflects when the row was re-inserted. This is the right (and only)
// behaviour for backup/restore. There used to be a regenerateIDs flag that
// swapped UUIDs but not NKeys, which produced a half-clone that couldn't
// coexist with the source (pubkey collision) and couldn't migrate either —
// it was dropped on 2026-05-13. A future "duplicate operator" feature would
// be a separate operation that also mints fresh NKeys and re-signs JWTs.
//
// Identity is established by operator ID, not name — exports preserve UUIDs,
// so two payloads with the same ID are by definition "the same operator".
//
// Behaviour:
//   - ID exists, overwrite=false → ErrOperatorImportExists.
//   - ID exists, overwrite=true → operator row updated in place; accounts
//     under the operator are deleted (FK cascades sweep scoped keys and
//     users) and recreated from the export. Clusters are **left alone** —
//     they model live NATS infrastructure (encrypted creds, health state,
//     server URLs) tied to the existing operator JWT, and restoring them
//     from a possibly-stale backup would clobber running state. The
//     exported.Clusters list is silently ignored on overwrite.
//   - ID does not exist, name collides on a different ID →
//     ErrOperatorImportNameConflict. Overwrite cannot resolve this; the
//     caller must rename or delete the conflicting operator first.
//   - Fresh import → operator + subtree + clusters all created.
//
// The whole flow runs inside a single tx so a mid-import error rolls
// everything back. Without that, an interrupted overwrite leaves the DB
// with the operator wiped but the subtree only partially restored.
func (s *ExportService) ImportOperator(ctx context.Context, exported *ExportedOperator, overwrite bool) error {
	if exported.Version != "1.0" {
		return fmt.Errorf("unsupported export version: %s", exported.Version)
	}

	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		operatorRepo := tx.OperatorRepository()
		accountRepo := tx.AccountRepository()
		userRepo := tx.UserRepository()
		scopedKeyRepo := tx.ScopedSigningKeyRepository()
		clusterRepo := tx.ClusterRepository()

		existing, err := operatorRepo.GetByID(ctx, exported.Operator.ID)
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return fmt.Errorf("failed to check existing operator by id: %w", err)
		}
		existsByID := existing != nil

		if existsByID && !overwrite {
			return fmt.Errorf("%w: operator %q (id=%s); pass overwrite=true to replace its subtree", ErrOperatorImportExists, existing.Name, existing.ID)
		}

		if !existsByID {
			// No row with this ID. If a different operator already owns the
			// name, refuse — same-name/different-ID is two operators, not one,
			// and overwrite can't disambiguate them.
			byName, err := operatorRepo.GetByName(ctx, exported.Operator.Name)
			if err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fmt.Errorf("failed to check existing operator by name: %w", err)
			}
			if byName != nil {
				return fmt.Errorf("%w: %q is owned by operator %s (import id=%s)", ErrOperatorImportNameConflict, exported.Operator.Name, byName.ID, exported.Operator.ID)
			}
		}

		opSeed, err := s.adoptSeed(ctx, exported.Operator.EncryptedSeed, exported.Operator.Seed, "operator "+exported.Operator.Name)
		if err != nil {
			return err
		}
		operator := &entities.Operator{
			ID:                  exported.Operator.ID,
			Name:                exported.Operator.Name,
			Description:         exported.Operator.Description,
			PublicKey:           exported.Operator.PublicKey,
			EncryptedSeed:       opSeed,
			SystemAccountPubKey: exported.Operator.SystemAccountPubKey,
			JWT:                 exported.Operator.JWT,
			CreatedAt:           exported.Operator.CreatedAt,
			UpdatedAt:           clock.Now(),
		}

		if existsByID {
			// Update operator row in place. We deliberately do NOT
			// delete-recreate it: clusters.operator_id is ON DELETE RESTRICT
			// (intentionally, see DeleteOperator), so a delete would fail
			// when clusters are attached. In-place update keeps cluster rows
			// pointing at a valid operator throughout the transaction.
			if err := operatorRepo.Update(ctx, operator); err != nil {
				return fmt.Errorf("failed to update operator: %w", err)
			}
			// Wipe the subtree. FK cascades on accounts sweep users and
			// scoped signing keys with them. Cluster rows are not touched
			// because they reference operator_id (which we just updated, not
			// deleted), not any account.
			existingAccounts, err := accountRepo.ListByOperator(ctx, exported.Operator.ID, repositories.ListOptions{Limit: 100000})
			if err != nil {
				return fmt.Errorf("failed to list existing accounts for overwrite: %w", err)
			}
			for _, acc := range existingAccounts {
				if err := accountRepo.Delete(ctx, acc.ID); err != nil {
					return fmt.Errorf("failed to delete existing account %s: %w", acc.ID, err)
				}
			}
		} else {
			if err := operatorRepo.Create(ctx, operator); err != nil {
				return fmt.Errorf("failed to create operator: %w", err)
			}
		}

		for _, exportedAccount := range exported.Accounts {
			accSeed, err := s.adoptSeed(ctx, exportedAccount.EncryptedSeed, exportedAccount.Seed, "account "+exportedAccount.Name)
			if err != nil {
				return err
			}
			account := &entities.Account{
				ID:                    exportedAccount.ID,
				OperatorID:            exported.Operator.ID,
				Name:                  exportedAccount.Name,
				Description:           exportedAccount.Description,
				PublicKey:             exportedAccount.PublicKey,
				EncryptedSeed:         accSeed,
				JetStreamEnabled:      exportedAccount.JetStreamEnabled,
				JetStreamMaxMemory:    exportedAccount.JetStreamMaxMemory,
				JetStreamMaxStorage:   exportedAccount.JetStreamMaxStorage,
				JetStreamMaxStreams:   exportedAccount.JetStreamMaxStreams,
				JetStreamMaxConsumers: exportedAccount.JetStreamMaxConsumers,
				JWT:                   exportedAccount.JWT,
				CreatedAt:             exportedAccount.CreatedAt,
				UpdatedAt:             clock.Now(),
			}
			if err := accountRepo.Create(ctx, account); err != nil {
				return fmt.Errorf("failed to create account %s: %w", exportedAccount.Name, err)
			}
		}

		for _, exportedKey := range exported.ScopedKeys {
			keySeed, err := s.adoptSeed(ctx, exportedKey.EncryptedSeed, exportedKey.Seed, "scoped key "+exportedKey.Name)
			if err != nil {
				return err
			}
			scopedKey := &entities.ScopedSigningKey{
				ID:              exportedKey.ID,
				AccountID:       exportedKey.AccountID,
				Name:            exportedKey.Name,
				Description:     exportedKey.Description,
				PublicKey:       exportedKey.PublicKey,
				EncryptedSeed:   keySeed,
				PubAllow:        exportedKey.PubAllow,
				PubDeny:         exportedKey.PubDeny,
				SubAllow:        exportedKey.SubAllow,
				SubDeny:         exportedKey.SubDeny,
				ResponseMaxMsgs: exportedKey.ResponseMaxMsgs,
				ResponseTTL:     exportedKey.ResponseTTL,
				CreatedAt:       exportedKey.CreatedAt,
				UpdatedAt:       clock.Now(),
			}
			if err := scopedKeyRepo.Create(ctx, scopedKey); err != nil {
				return fmt.Errorf("failed to create scoped key %s: %w", exportedKey.Name, err)
			}
		}

		for _, exportedUser := range exported.Users {
			userSeed, err := s.adoptSeed(ctx, exportedUser.EncryptedSeed, exportedUser.Seed, "user "+exportedUser.Name)
			if err != nil {
				return err
			}
			user := &entities.User{
				ID:                 exportedUser.ID,
				AccountID:          exportedUser.AccountID,
				Name:               exportedUser.Name,
				Description:        exportedUser.Description,
				PublicKey:          exportedUser.PublicKey,
				EncryptedSeed:      userSeed,
				JWT:                exportedUser.JWT,
				ScopedSigningKeyID: exportedUser.ScopedSigningKeyID,
				CreatedAt:          exportedUser.CreatedAt,
				UpdatedAt:          clock.Now(),
			}
			if err := userRepo.Create(ctx, user); err != nil {
				return fmt.Errorf("failed to create user %s: %w", exportedUser.Name, err)
			}
		}

		// Clusters are only restored on fresh imports. In overwrite mode the
		// existing cluster rows are deliberately left untouched (see the
		// function doc). The export's cluster list is silently dropped.
		if !existsByID {
			for _, exportedCluster := range exported.Clusters {
				clusterCreds, err := s.adoptSeed(ctx, exportedCluster.EncryptedCreds, exportedCluster.Creds, "cluster "+exportedCluster.Name)
				if err != nil {
					return err
				}
				cluster := &entities.Cluster{
					ID:                  exportedCluster.ID,
					OperatorID:          exported.Operator.ID,
					Name:                exportedCluster.Name,
					Description:         exportedCluster.Description,
					ServerURLs:          exportedCluster.ServerURLs,
					SystemAccountPubKey: exportedCluster.SystemAccountPubKey,
					EncryptedCreds:      clusterCreds,
					CreatedAt:           exportedCluster.CreatedAt,
					UpdatedAt:           clock.Now(),
				}
				if err := clusterRepo.Create(ctx, cluster); err != nil {
					return fmt.Errorf("failed to create cluster %s: %w", exportedCluster.Name, err)
				}
			}
		}

		return nil
	})
}

// adoptSeed returns the storage-ref the importer should persist for one entity,
// given the two mutually-exclusive seed fields from an export row:
//
//   - If `plaintext` is set, encrypt it with the destination's current
//     encryptor and return the resulting storage ref. This is the
//     "disaster-recovery into a fresh key" path.
//   - If only `encrypted` is set, return it verbatim — assumes the
//     destination uses the same encryption key the source used.
//   - If both are set, the export is ambiguous; refuse rather than guess.
//   - If neither is set (SecretsNone export), return "" — the entity row will
//     exist with a JWT and public key but no seed, so the server can verify
//     things signed by the original keys but cannot sign anything new for
//     that entity. Documented as the "metadata-only restore" mode; see
//     module docstring.
func (s *ExportService) adoptSeed(ctx context.Context, encrypted, plaintext, label string) (string, error) {
	if encrypted != "" && plaintext != "" {
		return "", fmt.Errorf("export for %s has both encrypted_seed and seed set; refusing ambiguous import", label)
	}
	if plaintext != "" {
		if s.encryptor == nil {
			return "", fmt.Errorf("cannot import plaintext seed for %s: no encryptor configured", label)
		}
		ref, err := s.encryptor.Encrypt(ctx, []byte(plaintext))
		if err != nil {
			return "", fmt.Errorf("failed to re-encrypt seed for %s: %w", label, err)
		}
		return ref, nil
	}
	return encrypted, nil
}

// ImportOperatorJSON imports an operator from JSON data. See ImportOperator
// for overwrite semantics.
func (s *ExportService) ImportOperatorJSON(ctx context.Context, data []byte, overwrite bool) error {
	var exported ExportedOperator
	if err := json.Unmarshal(data, &exported); err != nil {
		return fmt.Errorf("failed to unmarshal export: %w", err)
	}

	return s.ImportOperator(ctx, &exported, overwrite)
}

// ExportOperatorYAML exports an operator and returns YAML-encoded bytes.
func (s *ExportService) ExportOperatorYAML(ctx context.Context, operatorID uuid.UUID, mode SecretsMode) ([]byte, error) {
	exported, err := s.ExportOperator(ctx, operatorID, mode)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(exported)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal export to yaml: %w", err)
	}
	return data, nil
}

// ImportOperatorYAML imports an operator from YAML-encoded bytes. See
// ImportOperator for overwrite semantics.
func (s *ExportService) ImportOperatorYAML(ctx context.Context, data []byte, overwrite bool) error {
	var exported ExportedOperator
	if err := yaml.Unmarshal(data, &exported); err != nil {
		return fmt.Errorf("failed to unmarshal yaml export: %w", err)
	}
	return s.ImportOperator(ctx, &exported, overwrite)
}

// ExportOperatorBytes is a format-aware wrapper around the format-specific
// ExportOperator{JSON,YAML} methods. Empty format defaults to JSON for
// backward compatibility — older clients (and stored exports) predate yaml.
func (s *ExportService) ExportOperatorBytes(ctx context.Context, operatorID uuid.UUID, mode SecretsMode, format ExportFormat) ([]byte, error) {
	switch format {
	case "", FormatJSON:
		return s.ExportOperatorJSON(ctx, operatorID, mode)
	case FormatYAML:
		return s.ExportOperatorYAML(ctx, operatorID, mode)
	default:
		return nil, fmt.Errorf("unsupported export format: %q (want %q or %q)", format, FormatJSON, FormatYAML)
	}
}

// ImportOperatorBytes auto-detects JSON vs YAML by peeking at the first
// non-whitespace byte. A leading '{' or '[' is JSON; anything else is YAML.
// This lets clients write `cat export.{json,yaml} | nisctl import` without
// thinking about format. See ImportOperator for overwrite semantics.
func (s *ExportService) ImportOperatorBytes(ctx context.Context, data []byte, overwrite bool) error {
	exported, err := ParseExport(data)
	if err != nil {
		return err
	}
	return s.ImportOperator(ctx, exported, overwrite)
}

// ParseExport decodes the export bytes into an ExportedOperator struct,
// auto-detecting JSON vs YAML. Useful for callers that need to inspect the
// payload (e.g. to read the operator ID for a response) before kicking off
// the actual import.
func ParseExport(data []byte) (*ExportedOperator, error) {
	var exported ExportedOperator
	switch detectExportFormat(data) {
	case FormatJSON:
		if err := json.Unmarshal(data, &exported); err != nil {
			return nil, fmt.Errorf("failed to unmarshal json export: %w", err)
		}
	case FormatYAML:
		if err := yaml.Unmarshal(data, &exported); err != nil {
			return nil, fmt.Errorf("failed to unmarshal yaml export: %w", err)
		}
	default:
		return nil, fmt.Errorf("could not detect export format: input is empty or only whitespace")
	}
	return &exported, nil
}

// detectExportFormat sniffs the encoding from the first non-whitespace byte.
// JSON object/array start with '{' or '['; anything else (a YAML key, a '-'
// list marker, a '%' yaml directive, etc.) is treated as YAML.
func detectExportFormat(data []byte) ExportFormat {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return FormatJSON
		default:
			return FormatYAML
		}
	}
	return ""
}

// extractArchive / extractZipFile / extractTar moved to archive.go.

