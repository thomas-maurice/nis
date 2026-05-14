package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

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

// ExportedOperatorData contains operator data including encrypted seed
type ExportedOperatorData struct {
	ID                  uuid.UUID `json:"id" yaml:"id"`
	Name                string    `json:"name" yaml:"name"`
	Description         string    `json:"description" yaml:"description"`
	PublicKey           string    `json:"public_key" yaml:"public_key"`
	EncryptedSeed       string    `json:"encrypted_seed" yaml:"encrypted_seed"` // Re-encrypted with export key
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
	EncryptedSeed         string    `json:"encrypted_seed" yaml:"encrypted_seed"`
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
	EncryptedSeed   string        `json:"encrypted_seed" yaml:"encrypted_seed"`
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
	EncryptedSeed      string     `json:"encrypted_seed" yaml:"encrypted_seed"`
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
	EncryptedCreds      string    `json:"encrypted_creds" yaml:"encrypted_creds"`
	CreatedAt           time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt           time.Time `json:"updated_at" yaml:"updated_at"`
}

// ExportOperator exports an operator and all its associated data
func (s *ExportService) ExportOperator(ctx context.Context, operatorID uuid.UUID, includeSecrets bool) (*ExportedOperator, error) {
	// Get operator
	operator, err := s.operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	exported := &ExportedOperator{
		Version:    "1.0",
		ExportedAt: time.Now(),
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

	// Include encrypted seed if secrets are requested
	if includeSecrets {
		exported.Operator.EncryptedSeed = operator.EncryptedSeed
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

		if includeSecrets {
			exportedAccount.EncryptedSeed = account.EncryptedSeed
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

			if includeSecrets {
				exportedKey.EncryptedSeed = key.EncryptedSeed
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

			if includeSecrets {
				exportedUser.EncryptedSeed = user.EncryptedSeed
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

		if includeSecrets {
			exportedCluster.EncryptedCreds = cluster.EncryptedCreds
		}

		exported.Clusters = append(exported.Clusters, exportedCluster)
	}

	return exported, nil
}

// ExportOperatorJSON exports an operator to JSON
func (s *ExportService) ExportOperatorJSON(ctx context.Context, operatorID uuid.UUID, includeSecrets bool) ([]byte, error) {
	exported, err := s.ExportOperator(ctx, operatorID, includeSecrets)
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
func (s *ExportService) ImportOperator(ctx context.Context, exported *ExportedOperator) error {
	if exported.Version != "1.0" {
		return fmt.Errorf("unsupported export version: %s", exported.Version)
	}

	// Check if operator with this name already exists
	existing, err := s.operatorRepo.GetByName(ctx, exported.Operator.Name)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return fmt.Errorf("failed to check existing operator: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("operator with name '%s' already exists", exported.Operator.Name)
	}

	operator := &entities.Operator{
		ID:                  exported.Operator.ID,
		Name:                exported.Operator.Name,
		Description:         exported.Operator.Description,
		PublicKey:           exported.Operator.PublicKey,
		EncryptedSeed:       exported.Operator.EncryptedSeed,
		SystemAccountPubKey: exported.Operator.SystemAccountPubKey,
		JWT:                 exported.Operator.JWT,
		CreatedAt:           exported.Operator.CreatedAt,
		UpdatedAt:           time.Now(),
	}

	if err := s.operatorRepo.Create(ctx, operator); err != nil {
		return fmt.Errorf("failed to create operator: %w", err)
	}

	for _, exportedAccount := range exported.Accounts {
		account := &entities.Account{
			ID:                    exportedAccount.ID,
			OperatorID:            exported.Operator.ID,
			Name:                  exportedAccount.Name,
			Description:           exportedAccount.Description,
			PublicKey:             exportedAccount.PublicKey,
			EncryptedSeed:         exportedAccount.EncryptedSeed,
			JetStreamEnabled:      exportedAccount.JetStreamEnabled,
			JetStreamMaxMemory:    exportedAccount.JetStreamMaxMemory,
			JetStreamMaxStorage:   exportedAccount.JetStreamMaxStorage,
			JetStreamMaxStreams:   exportedAccount.JetStreamMaxStreams,
			JetStreamMaxConsumers: exportedAccount.JetStreamMaxConsumers,
			JWT:                   exportedAccount.JWT,
			CreatedAt:             exportedAccount.CreatedAt,
			UpdatedAt:             time.Now(),
		}
		if err := s.accountRepo.Create(ctx, account); err != nil {
			return fmt.Errorf("failed to create account %s: %w", exportedAccount.Name, err)
		}
	}

	for _, exportedKey := range exported.ScopedKeys {
		scopedKey := &entities.ScopedSigningKey{
			ID:              exportedKey.ID,
			AccountID:       exportedKey.AccountID,
			Name:            exportedKey.Name,
			Description:     exportedKey.Description,
			PublicKey:       exportedKey.PublicKey,
			EncryptedSeed:   exportedKey.EncryptedSeed,
			PubAllow:        exportedKey.PubAllow,
			PubDeny:         exportedKey.PubDeny,
			SubAllow:        exportedKey.SubAllow,
			SubDeny:         exportedKey.SubDeny,
			ResponseMaxMsgs: exportedKey.ResponseMaxMsgs,
			ResponseTTL:     exportedKey.ResponseTTL,
			CreatedAt:       exportedKey.CreatedAt,
			UpdatedAt:       time.Now(),
		}
		if err := s.scopedKeyRepo.Create(ctx, scopedKey); err != nil {
			return fmt.Errorf("failed to create scoped key %s: %w", exportedKey.Name, err)
		}
	}

	for _, exportedUser := range exported.Users {
		user := &entities.User{
			ID:                 exportedUser.ID,
			AccountID:          exportedUser.AccountID,
			Name:               exportedUser.Name,
			Description:        exportedUser.Description,
			PublicKey:          exportedUser.PublicKey,
			EncryptedSeed:      exportedUser.EncryptedSeed,
			JWT:                exportedUser.JWT,
			ScopedSigningKeyID: exportedUser.ScopedSigningKeyID,
			CreatedAt:          exportedUser.CreatedAt,
			UpdatedAt:          time.Now(),
		}
		if err := s.userRepo.Create(ctx, user); err != nil {
			return fmt.Errorf("failed to create user %s: %w", exportedUser.Name, err)
		}
	}

	for _, exportedCluster := range exported.Clusters {
		cluster := &entities.Cluster{
			ID:                  exportedCluster.ID,
			OperatorID:          exported.Operator.ID,
			Name:                exportedCluster.Name,
			Description:         exportedCluster.Description,
			ServerURLs:          exportedCluster.ServerURLs,
			SystemAccountPubKey: exportedCluster.SystemAccountPubKey,
			EncryptedCreds:      exportedCluster.EncryptedCreds,
			CreatedAt:           exportedCluster.CreatedAt,
			UpdatedAt:           time.Now(),
		}
		if err := s.clusterRepo.Create(ctx, cluster); err != nil {
			return fmt.Errorf("failed to create cluster %s: %w", exportedCluster.Name, err)
		}
	}

	return nil
}

// ImportOperatorJSON imports an operator from JSON data
func (s *ExportService) ImportOperatorJSON(ctx context.Context, data []byte) error {
	var exported ExportedOperator
	if err := json.Unmarshal(data, &exported); err != nil {
		return fmt.Errorf("failed to unmarshal export: %w", err)
	}

	return s.ImportOperator(ctx, &exported)
}

// ExportOperatorYAML exports an operator and returns YAML-encoded bytes.
func (s *ExportService) ExportOperatorYAML(ctx context.Context, operatorID uuid.UUID, includeSecrets bool) ([]byte, error) {
	exported, err := s.ExportOperator(ctx, operatorID, includeSecrets)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(exported)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal export to yaml: %w", err)
	}
	return data, nil
}

// ImportOperatorYAML imports an operator from YAML-encoded bytes.
func (s *ExportService) ImportOperatorYAML(ctx context.Context, data []byte) error {
	var exported ExportedOperator
	if err := yaml.Unmarshal(data, &exported); err != nil {
		return fmt.Errorf("failed to unmarshal yaml export: %w", err)
	}
	return s.ImportOperator(ctx, &exported)
}

// ExportOperatorBytes is a format-aware wrapper around the format-specific
// ExportOperator{JSON,YAML} methods. Empty format defaults to JSON for
// backward compatibility — older clients (and stored exports) predate yaml.
func (s *ExportService) ExportOperatorBytes(ctx context.Context, operatorID uuid.UUID, includeSecrets bool, format ExportFormat) ([]byte, error) {
	switch format {
	case "", FormatJSON:
		return s.ExportOperatorJSON(ctx, operatorID, includeSecrets)
	case FormatYAML:
		return s.ExportOperatorYAML(ctx, operatorID, includeSecrets)
	default:
		return nil, fmt.Errorf("unsupported export format: %q (want %q or %q)", format, FormatJSON, FormatYAML)
	}
}

// ImportOperatorBytes auto-detects JSON vs YAML by peeking at the first
// non-whitespace byte. A leading '{' or '[' is JSON; anything else is YAML.
// This lets clients write `cat export.{json,yaml} | nisctl import` without
// thinking about format.
func (s *ExportService) ImportOperatorBytes(ctx context.Context, data []byte) error {
	exported, err := ParseExport(data)
	if err != nil {
		return err
	}
	return s.ImportOperator(ctx, exported)
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

