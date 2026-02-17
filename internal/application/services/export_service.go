package services

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

// ExportService provides business logic for exporting and importing operators
type ExportService struct {
	operatorRepo    repositories.OperatorRepository
	accountRepo     repositories.AccountRepository
	userRepo        repositories.UserRepository
	scopedKeyRepo   repositories.ScopedSigningKeyRepository
	clusterRepo     repositories.ClusterRepository
	operatorService *OperatorService
	accountService  *AccountService
	userService     *UserService
	scopedKeyService *ScopedSigningKeyService
	clusterService  *ClusterService
	encryptor       encryption.Encryptor
}

// NewExportService creates a new export service
func NewExportService(
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

// ExportedOperator represents a complete export of an operator and all its data
type ExportedOperator struct {
	Version     string                       `json:"version"`
	ExportedAt  time.Time                    `json:"exported_at"`
	Operator    *ExportedOperatorData        `json:"operator"`
	Accounts    []*ExportedAccountData       `json:"accounts"`
	ScopedKeys  []*ExportedScopedKeyData     `json:"scoped_keys"`
	Users       []*ExportedUserData          `json:"users"`
	Clusters    []*ExportedClusterData       `json:"clusters,omitempty"`
}

// ExportedOperatorData contains operator data including encrypted seed
type ExportedOperatorData struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	PublicKey           string    `json:"public_key"`
	EncryptedSeed       string    `json:"encrypted_seed"` // Re-encrypted with export key
	SystemAccountPubKey string    `json:"system_account_pub_key"`
	JWT                 string    `json:"jwt"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ExportedAccountData contains account data
type ExportedAccountData struct {
	ID                    uuid.UUID     `json:"id"`
	OperatorID            uuid.UUID     `json:"operator_id"`
	Name                  string        `json:"name"`
	Description           string        `json:"description"`
	PublicKey             string        `json:"public_key"`
	EncryptedSeed         string        `json:"encrypted_seed"`
	JetStreamEnabled      bool          `json:"jetstream_enabled"`
	JetStreamMaxMemory    int64         `json:"jetstream_max_memory"`
	JetStreamMaxStorage   int64         `json:"jetstream_max_storage"`
	JetStreamMaxStreams   int64         `json:"jetstream_max_streams"`
	JetStreamMaxConsumers int64         `json:"jetstream_max_consumers"`
	JWT                   string        `json:"jwt"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
}

// ExportedScopedKeyData contains scoped signing key data
type ExportedScopedKeyData struct {
	ID              uuid.UUID     `json:"id"`
	AccountID       uuid.UUID     `json:"account_id"`
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	PublicKey       string        `json:"public_key"`
	EncryptedSeed   string        `json:"encrypted_seed"`
	PubAllow        []string      `json:"pub_allow"`
	PubDeny         []string      `json:"pub_deny"`
	SubAllow        []string      `json:"sub_allow"`
	SubDeny         []string      `json:"sub_deny"`
	ResponseMaxMsgs int           `json:"response_max_msgs"`
	ResponseTTL     time.Duration `json:"response_ttl"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// ExportedUserData contains user data
type ExportedUserData struct {
	ID                  uuid.UUID  `json:"id"`
	AccountID           uuid.UUID  `json:"account_id"`
	Name                string     `json:"name"`
	Description         string     `json:"description"`
	PublicKey           string     `json:"public_key"`
	EncryptedSeed       string     `json:"encrypted_seed"`
	JWT                 string     `json:"jwt"`
	ScopedSigningKeyID  *uuid.UUID `json:"scoped_signing_key_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ExportedClusterData contains cluster data
type ExportedClusterData struct {
	ID                   uuid.UUID `json:"id"`
	OperatorID           uuid.UUID `json:"operator_id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	ServerURLs           []string  `json:"server_urls"`
	SystemAccountPubKey  string    `json:"system_account_pub_key"`
	EncryptedCreds       string    `json:"encrypted_creds"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
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

// ImportOperator imports an operator from exported data
func (s *ExportService) ImportOperator(ctx context.Context, exported *ExportedOperator, regenerateIDs bool) error {
	if exported.Version != "1.0" {
		return fmt.Errorf("unsupported export version: %s", exported.Version)
	}

	// Map old IDs to new IDs if regenerating
	idMap := make(map[uuid.UUID]uuid.UUID)

	// Import operator
	operatorID := exported.Operator.ID
	if regenerateIDs {
		operatorID = uuid.New()
		idMap[exported.Operator.ID] = operatorID
	}

	// Check if operator with this name already exists
	existing, err := s.operatorRepo.GetByName(ctx, exported.Operator.Name)
	if err != nil && err != repositories.ErrNotFound {
		return fmt.Errorf("failed to check existing operator: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("operator with name '%s' already exists", exported.Operator.Name)
	}

	operator := &entities.Operator{
		ID:                  operatorID,
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

	// Import accounts
	for _, exportedAccount := range exported.Accounts {
		accountID := exportedAccount.ID
		if regenerateIDs {
			accountID = uuid.New()
			idMap[exportedAccount.ID] = accountID
		}

		account := &entities.Account{
			ID:                    accountID,
			OperatorID:            operatorID,
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

	// Import scoped signing keys
	for _, exportedKey := range exported.ScopedKeys {
		keyID := exportedKey.ID
		accountID := exportedKey.AccountID

		if regenerateIDs {
			keyID = uuid.New()
			idMap[exportedKey.ID] = keyID
			if newAccountID, ok := idMap[exportedKey.AccountID]; ok {
				accountID = newAccountID
			}
		}

		scopedKey := &entities.ScopedSigningKey{
			ID:              keyID,
			AccountID:       accountID,
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

	// Import users
	for _, exportedUser := range exported.Users {
		userID := exportedUser.ID
		accountID := exportedUser.AccountID
		var scopedKeyID *uuid.UUID

		if regenerateIDs {
			userID = uuid.New()
			idMap[exportedUser.ID] = userID
			if newAccountID, ok := idMap[exportedUser.AccountID]; ok {
				accountID = newAccountID
			}
			if exportedUser.ScopedSigningKeyID != nil {
				if newKeyID, ok := idMap[*exportedUser.ScopedSigningKeyID]; ok {
					scopedKeyID = &newKeyID
				}
			}
		} else {
			scopedKeyID = exportedUser.ScopedSigningKeyID
		}

		user := &entities.User{
			ID:                 userID,
			AccountID:          accountID,
			Name:               exportedUser.Name,
			Description:        exportedUser.Description,
			PublicKey:          exportedUser.PublicKey,
			EncryptedSeed:      exportedUser.EncryptedSeed,
			JWT:                exportedUser.JWT,
			ScopedSigningKeyID: scopedKeyID,
			CreatedAt:          exportedUser.CreatedAt,
			UpdatedAt:          time.Now(),
		}

		if err := s.userRepo.Create(ctx, user); err != nil {
			return fmt.Errorf("failed to create user %s: %w", exportedUser.Name, err)
		}
	}

	// Import clusters
	for _, exportedCluster := range exported.Clusters {
		clusterID := exportedCluster.ID

		if regenerateIDs {
			clusterID = uuid.New()
		}

		cluster := &entities.Cluster{
			ID:                  clusterID,
			OperatorID:          operatorID,
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
func (s *ExportService) ImportOperatorJSON(ctx context.Context, data []byte, regenerateIDs bool) error {
	var exported ExportedOperator
	if err := json.Unmarshal(data, &exported); err != nil {
		return fmt.Errorf("failed to unmarshal export: %w", err)
	}

	return s.ImportOperator(ctx, &exported, regenerateIDs)
}

// extractArchive extracts a compressed archive to a temporary directory
func (s *ExportService) extractArchive(archiveData []byte) (string, error) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "nsc-import-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Try to detect and extract the archive type
	reader := bytes.NewReader(archiveData)

	// Try ZIP first
	zipReader, err := zip.NewReader(reader, int64(len(archiveData)))
	if err == nil {
		// Extract ZIP
		for _, file := range zipReader.File {
			if err := extractZipFile(file, tempDir); err != nil {
				_ = os.RemoveAll(tempDir)
				return "", fmt.Errorf("failed to extract zip file: %w", err)
			}
		}
		return tempDir, nil
	}

	// Try gzip + tar
	_, _ = reader.Seek(0, io.SeekStart)
	gzipReader, err := gzip.NewReader(reader)
	if err == nil {
		defer func() { _ = gzipReader.Close() }()
		if err := extractTar(gzipReader, tempDir); err == nil {
			return tempDir, nil
		}
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to extract tar.gz: %w", err)
	}

	// Try bzip2 + tar
	_, _ = reader.Seek(0, io.SeekStart)
	bz2Reader := bzip2.NewReader(reader)
	if err := extractTar(bz2Reader, tempDir); err == nil {
		return tempDir, nil
	}

	_ = os.RemoveAll(tempDir)
	return "", fmt.Errorf("unsupported archive format (supported: .zip, .tar.gz, .tar.bz2)")
}

// extractZipFile extracts a single file from a ZIP archive
func extractZipFile(file *zip.File, destDir string) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	path := filepath.Join(destDir, file.Name)

	if file.FileInfo().IsDir() {
		return os.MkdirAll(path, file.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(f, rc)
	return err
}

// extractTar extracts a tar archive
func extractTar(reader io.Reader, destDir string) error {
	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		path := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tarReader); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
	}

	return nil
}

// ImportFromNSC imports an operator from an NSC archive
// The archive should contain the NSC store structure:
// operator/operator.jwt
// operator/accounts/{accountName}/{accountName}.jwt
// operator/accounts/{accountName}/users/{userName}.jwt
// nkeys/keys/{type}/{prefix}/{fullkey}.nk
func (s *ExportService) ImportFromNSC(ctx context.Context, archiveData []byte, operatorName string) (uuid.UUID, error) {
	// Extract archive to temp directory
	tempDir, err := s.extractArchive(archiveData)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to extract archive: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Find the NSC store directory (may be nested in the archive)
	nscDir := tempDir
	operatorDir := filepath.Join(tempDir, "operator")
	if _, err := os.Stat(operatorDir); err != nil {
		// Try to find operator directory in subdirectories
		entries, err := os.ReadDir(tempDir)
		if err == nil && len(entries) == 1 && entries[0].IsDir() {
			// If there's only one directory, it might be the NSC store
			potentialOperatorDir := filepath.Join(tempDir, entries[0].Name(), "operator")
			if _, err := os.Stat(potentialOperatorDir); err == nil {
				nscDir = filepath.Join(tempDir, entries[0].Name())
			}
		}
	}

	// Read operator JWT - always at operator/operator.jwt
	operatorJWTPath := filepath.Join(nscDir, "operator", "operator.jwt")
	operatorJWTData, err := os.ReadFile(operatorJWTPath)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to read operator JWT at %s: %w", operatorJWTPath, err)
	}

	// Parse operator JWT
	operatorClaims, err := jwt.DecodeOperatorClaims(string(operatorJWTData))
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to decode operator claims: %w", err)
	}

	// Get operator public key from JWT
	operatorPubKey := operatorClaims.Subject

	// Find operator seed in nkeys/keys/O/{prefix}/{fullkey}.nk
	operatorSeedData, err := s.findNKey(nscDir, operatorPubKey)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to find operator seed for %s: %w", operatorPubKey, err)
	}

	// Parse the seed
	operatorKeyPair, err := nkeys.FromSeed(operatorSeedData)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to parse operator seed: %w", err)
	}

	// Verify public key matches
	verifyPubKey, err := operatorKeyPair.PublicKey()
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to get operator public key: %w", err)
	}
	if verifyPubKey != operatorPubKey {
		return uuid.Nil, fmt.Errorf("operator public key mismatch: expected %s, got %s", operatorPubKey, verifyPubKey)
	}

	// Encrypt the seed for storage
	encryptedSeed, err := s.encryptor.Encrypt(ctx, operatorSeedData)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to encrypt operator seed: %w", err)
	}

	// Convert tags to description
	description := ""
	if len(operatorClaims.Tags) > 0 {
		description = strings.Join(operatorClaims.Tags, ", ")
	}

	// Use provided operator name if given, otherwise use name from JWT
	finalOperatorName := operatorName
	if finalOperatorName == "" {
		finalOperatorName = operatorClaims.Name
	}

	// Create operator entity (without system account yet, will be set after SYS account is imported)
	operatorID := uuid.New()
	operator := &entities.Operator{
		ID:            operatorID,
		Name:          finalOperatorName,
		Description:   description,
		EncryptedSeed: encryptedSeed,
		PublicKey:     operatorPubKey,
		JWT:           string(operatorJWTData), // Use original JWT from NSC
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Save operator
	if err := s.operatorRepo.Create(ctx, operator); err != nil {
		return uuid.Nil, fmt.Errorf("failed to create operator: %w", err)
	}

	// TODO: Import operator scoped signing keys if present
	// For now we preserve the original JWTs from NSC, so we don't need to
	// import operator signing keys. In the future, if we want to create NEW
	// accounts using these keys, we would need to store them.

	// Track the SYS account public key for later
	var sysAccountPubKey string

	// Find and import accounts - in operator/accounts/
	accountsDir := filepath.Join(nscDir, "operator", "accounts")
	if _, err := os.Stat(accountsDir); err == nil {
		accountEntries, err := os.ReadDir(accountsDir)
		if err != nil {
			return uuid.Nil, fmt.Errorf("failed to read accounts directory: %w", err)
		}

		for _, accountEntry := range accountEntries {
			if !accountEntry.IsDir() {
				continue
			}

			accountName := accountEntry.Name()
			accountPubKey, err := s.importNSCAccount(ctx, nscDir, operatorID, operatorKeyPair, accountName)
			if err != nil {
				return uuid.Nil, fmt.Errorf("failed to import account %s: %w", accountName, err)
			}

			// Track SYS or $SYS account
			if accountName == "SYS" || accountName == "$SYS" {
				sysAccountPubKey = accountPubKey
			}
		}
	}

	// Set system account if $SYS was found
	if sysAccountPubKey != "" {
		if _, err := s.operatorService.SetSystemAccount(ctx, operatorID, sysAccountPubKey); err != nil {
			return uuid.Nil, fmt.Errorf("failed to set system account: %w", err)
		}

		// Create system user in $SYS account if it doesn't exist
		if err := s.ensureSystemUser(ctx, operatorID); err != nil {
			return uuid.Nil, fmt.Errorf("failed to ensure system user: %w", err)
		}
	}

	return operatorID, nil
}

// ensureSystemUser creates a system user in the system account if it doesn't exist
func (s *ExportService) ensureSystemUser(ctx context.Context, operatorID uuid.UUID) error {
	// Get the operator to find the system account public key
	operator, err := s.operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}

	if operator.SystemAccountPubKey == "" {
		return fmt.Errorf("operator has no system account configured")
	}

	// Find the system account by matching public key
	accounts, err := s.accountRepo.ListByOperator(ctx, operatorID, repositories.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list accounts: %w", err)
	}

	var sysAccount *entities.Account
	for _, account := range accounts {
		if account.PublicKey == operator.SystemAccountPubKey {
			sysAccount = account
			break
		}
	}

	if sysAccount == nil {
		return fmt.Errorf("system account not found")
	}

	// Check if system user already exists in system account
	users, err := s.userRepo.ListByAccount(ctx, sysAccount.ID, repositories.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list users in system account: %w", err)
	}

	var systemUser *entities.User
	// Look specifically for a user named "system"
	for _, user := range users {
		if user.Name == "system" {
			systemUser = user
			break
		}
	}

	// Create system user if it doesn't exist
	// This ensures consistency - we always use a user named "system" for cluster management
	if systemUser == nil {
		// Check if the account has scoped signing keys - if so, use the first one
		// This is important for imported NSC operators that use scoped signing keys
		var scopedKeyID *uuid.UUID
		scopedKeys, err := s.scopedKeyRepo.ListByAccount(ctx, sysAccount.ID, repositories.ListOptions{Limit: 1})
		if err == nil && len(scopedKeys) > 0 {
			scopedKeyID = &scopedKeys[0].ID
		}

		userReq := CreateUserRequest{
			AccountID:         sysAccount.ID,
			Name:              "system",
			Description:       "System user for operator management",
			ScopedSigningKeyID: scopedKeyID,
		}

		systemUser, err = s.userService.CreateUser(ctx, userReq)
		if err != nil {
			return fmt.Errorf("failed to create system user: %w", err)
		}
	}

	// Update all clusters for this operator to use the system user credentials
	if err := s.updateClustersWithSystemUser(ctx, operatorID, systemUser.ID); err != nil {
		// Log but don't fail - credentials can be set later
		// This is not critical for the import to succeed
		fmt.Printf("Warning: failed to update cluster credentials: %v\n", err)
	}

	return nil
}

// updateClustersWithSystemUser updates all clusters for an operator to use the system user
func (s *ExportService) updateClustersWithSystemUser(ctx context.Context, operatorID, systemUserID uuid.UUID) error {
	// Get all clusters for this operator
	clusters, err := s.clusterRepo.ListByOperator(ctx, operatorID, repositories.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	// Update credentials for each cluster
	for _, cluster := range clusters {
		_, err := s.clusterService.UpdateClusterCredentials(ctx, cluster.ID, systemUserID)
		if err != nil {
			return fmt.Errorf("failed to update credentials for cluster %s: %w", cluster.Name, err)
		}
	}

	return nil
}

// findNKey locates an nkey file in the NSC store structure
// NSC stores keys at: nkeys/keys/{type}/{prefix}/{publicKey}.nk
// where type is O (operator), A (account), or U (user)
// and prefix is the first 2 characters after the type prefix
func (s *ExportService) findNKey(nscDir string, publicKey string) ([]byte, error) {
	if len(publicKey) < 3 {
		return nil, fmt.Errorf("invalid public key length: %s", publicKey)
	}

	// Extract type and prefix
	keyType := string(publicKey[0])
	prefix := publicKey[1:3]

	// Construct path: nkeys/keys/{type}/{prefix}/{fullkey}.nk
	keyPath := filepath.Join(nscDir, "nkeys", "keys", keyType, prefix, publicKey+".nk")

	// Read the key file
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read nkey at %s: %w", keyPath, err)
	}

	return keyData, nil
}

// importNSCAccount imports a single account from NSC and returns the account public key
func (s *ExportService) importNSCAccount(ctx context.Context, nscDir string, operatorID uuid.UUID, operatorKeyPair nkeys.KeyPair, accountName string) (string, error) {
	// Read account JWT - in operator/accounts/{accountName}/{accountName}.jwt
	accountJWTPath := filepath.Join(nscDir, "operator", "accounts", accountName, accountName+".jwt")
	accountJWTData, err := os.ReadFile(accountJWTPath)
	if err != nil {
		return "", fmt.Errorf("failed to read account JWT: %w", err)
	}

	// Parse account JWT
	accountClaims, err := jwt.DecodeAccountClaims(string(accountJWTData))
	if err != nil {
		return "", fmt.Errorf("failed to decode account claims: %w", err)
	}

	// Get account public key from JWT
	accountPubKey := accountClaims.Subject

	// Find account seed in nkeys/keys/A/{prefix}/{fullkey}.nk
	accountSeedData, err := s.findNKey(nscDir, accountPubKey)
	if err != nil {
		return "", fmt.Errorf("failed to find account seed for %s: %w", accountPubKey, err)
	}

	// Parse the seed
	accountKeyPair, err := nkeys.FromSeed(accountSeedData)
	if err != nil {
		return "", fmt.Errorf("failed to parse account seed: %w", err)
	}

	// Verify public key matches
	verifyPubKey, err := accountKeyPair.PublicKey()
	if err != nil {
		return "", fmt.Errorf("failed to get account public key: %w", err)
	}
	if verifyPubKey != accountPubKey {
		return "", fmt.Errorf("account public key mismatch: expected %s, got %s", accountPubKey, verifyPubKey)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, accountSeedData)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt account seed: %w", err)
	}

	// Convert tags to description
	description := ""
	if len(accountClaims.Tags) > 0 {
		description = strings.Join(accountClaims.Tags, ", ")
	}

	// Create account entity
	accountID := uuid.New()
	account := &entities.Account{
		ID:                    accountID,
		OperatorID:            operatorID,
		Name:                  accountClaims.Name,
		Description:           description,
		EncryptedSeed:         encryptedSeed,
		PublicKey:             accountPubKey,
		JetStreamEnabled:      accountClaims.Limits.DiskStorage != 0 || accountClaims.Limits.MemoryStorage != 0,
		JetStreamMaxMemory:    accountClaims.Limits.MemoryStorage,
		JetStreamMaxStorage:   accountClaims.Limits.DiskStorage,
		JetStreamMaxStreams:   int64(accountClaims.Limits.Streams),
		JetStreamMaxConsumers: int64(accountClaims.Limits.Consumer),
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	// Use the original account JWT from NSC (don't re-sign it)
	// This preserves the original signature and any scoped signing key relationships
	account.JWT = string(accountJWTData)

	// Save account
	if err := s.accountRepo.Create(ctx, account); err != nil {
		return "", fmt.Errorf("failed to create account: %w", err)
	}

	// Get operator for user imports
	operator, err := s.operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return "", fmt.Errorf("failed to get operator: %w", err)
	}

	// Import scoped signing keys from the account JWT
	if len(accountClaims.SigningKeys) > 0 {
		for signingKeyPubKey := range accountClaims.SigningKeys {
			if err := s.importNSCScopedSigningKey(ctx, nscDir, accountID, signingKeyPubKey); err != nil {
				return "", fmt.Errorf("failed to import scoped signing key %s: %w", signingKeyPubKey, err)
			}
		}
	}

	// Find and import users - in operator/accounts/{accountName}/users/
	usersDir := filepath.Join(nscDir, "operator", "accounts", accountName, "users")
	if _, err := os.Stat(usersDir); err == nil {
		userEntries, err := os.ReadDir(usersDir)
		if err != nil {
			return "", fmt.Errorf("failed to read users directory: %w", err)
		}

		for _, userEntry := range userEntries {
			if userEntry.IsDir() || !strings.HasSuffix(userEntry.Name(), ".jwt") {
				continue
			}

			userName := strings.TrimSuffix(userEntry.Name(), ".jwt")
			if err := s.importNSCUser(ctx, nscDir, accountID, accountName, userName, operator); err != nil {
				return "", fmt.Errorf("failed to import user %s: %w", userName, err)
			}
		}
	}

	return accountPubKey, nil
}

// importNSCScopedSigningKey imports a scoped signing key from NSC
func (s *ExportService) importNSCScopedSigningKey(ctx context.Context, nscDir string, accountID uuid.UUID, signingKeyPubKey string) error {
	// Find signing key seed in nkeys/keys/A/{prefix}/{fullkey}.nk
	signingKeySeedData, err := s.findNKey(nscDir, signingKeyPubKey)
	if err != nil {
		return fmt.Errorf("failed to find scoped signing key seed for %s: %w", signingKeyPubKey, err)
	}

	// Parse the seed
	signingKeyPair, err := nkeys.FromSeed(signingKeySeedData)
	if err != nil {
		return fmt.Errorf("failed to parse scoped signing key seed: %w", err)
	}

	// Verify public key matches
	verifyPubKey, err := signingKeyPair.PublicKey()
	if err != nil {
		return fmt.Errorf("failed to get scoped signing key public key: %w", err)
	}
	if verifyPubKey != signingKeyPubKey {
		return fmt.Errorf("scoped signing key public key mismatch: expected %s, got %s", signingKeyPubKey, verifyPubKey)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, signingKeySeedData)
	if err != nil {
		return fmt.Errorf("failed to encrypt scoped signing key seed: %w", err)
	}

	// Create scoped signing key entity
	// Note: NSC doesn't store permission templates for signing keys separately,
	// they're only defined when used to sign user JWTs. We'll create the key
	// with empty permissions - they'll be populated when users reference this key.
	scopedKeyID := uuid.New()
	scopedKey := &entities.ScopedSigningKey{
		ID:            scopedKeyID,
		AccountID:     accountID,
		Name:          fmt.Sprintf("imported-key-%s", signingKeyPubKey[1:5]), // Use first few chars for name
		Description:   "Scoped signing key imported from NSC",
		EncryptedSeed: encryptedSeed,
		PublicKey:     signingKeyPubKey,
		PubAllow:      []string{},
		PubDeny:       []string{},
		SubAllow:      []string{},
		SubDeny:       []string{},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Save scoped signing key
	if err := s.scopedKeyRepo.Create(ctx, scopedKey); err != nil {
		return fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	return nil
}

// importNSCUser imports a single user from NSC
// Note: NSC users store their permissions in the JWT, but NIS users get permissions
// from scoped signing keys. We create the user without a scoped key and let the
// user service generate the JWT with default permissions from the account.
func (s *ExportService) importNSCUser(ctx context.Context, nscDir string, accountID uuid.UUID, accountName string, userName string, operator *entities.Operator) error {
	// Read user JWT - in operator/accounts/{accountName}/users/{userName}.jwt
	userJWTPath := filepath.Join(nscDir, "operator", "accounts", accountName, "users", userName+".jwt")
	userJWTData, err := os.ReadFile(userJWTPath)
	if err != nil {
		return fmt.Errorf("failed to read user JWT: %w", err)
	}

	// Parse user JWT to get metadata
	userClaims, err := jwt.DecodeUserClaims(string(userJWTData))
	if err != nil {
		return fmt.Errorf("failed to decode user claims: %w", err)
	}

	// Get user public key from JWT
	userPubKey := userClaims.Subject

	// Find user seed in nkeys/keys/U/{prefix}/{fullkey}.nk
	userSeedData, err := s.findNKey(nscDir, userPubKey)
	if err != nil {
		return fmt.Errorf("failed to find user seed for %s: %w", userPubKey, err)
	}

	// Parse the seed
	userKeyPair, err := nkeys.FromSeed(userSeedData)
	if err != nil {
		return fmt.Errorf("failed to parse user seed: %w", err)
	}

	// Verify public key matches
	verifyPubKey, err := userKeyPair.PublicKey()
	if err != nil {
		return fmt.Errorf("failed to get user public key: %w", err)
	}
	if verifyPubKey != userPubKey {
		return fmt.Errorf("user public key mismatch: expected %s, got %s", userPubKey, verifyPubKey)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, userSeedData)
	if err != nil {
		return fmt.Errorf("failed to encrypt user seed: %w", err)
	}

	// Convert tags to description
	description := ""
	if len(userClaims.Tags) > 0 {
		description = strings.Join(userClaims.Tags, ", ")
	}

	// If user has custom permissions, create a scoped signing key for them
	var scopedKeyID *uuid.UUID
	hasCustomPermissions := len(userClaims.Pub.Allow) > 0 || len(userClaims.Pub.Deny) > 0 ||
		len(userClaims.Sub.Allow) > 0 || len(userClaims.Sub.Deny) > 0 ||
		userClaims.Resp != nil && (userClaims.Resp.MaxMsgs > 0 || userClaims.Resp.Expires > 0)

	if hasCustomPermissions {
		// Create a scoped signing key with the user's permissions
		keyID := uuid.New()
		scopedKeyID = &keyID

		// For NSC import, we don't have the signing key seed, so we create a new one
		// and use it to sign future JWTs for this user pattern
		signingKeyPair, err := nkeys.CreateAccount()
		if err != nil {
			return fmt.Errorf("failed to create signing key pair: %w", err)
		}

		signingKeySeed, err := signingKeyPair.Seed()
		if err != nil {
			return fmt.Errorf("failed to get signing key seed: %w", err)
		}

		signingKeyPubKey, err := signingKeyPair.PublicKey()
		if err != nil {
			return fmt.Errorf("failed to get signing key public key: %w", err)
		}

		encryptedSigningSeed, err := s.encryptor.Encrypt(ctx, signingKeySeed)
		if err != nil {
			return fmt.Errorf("failed to encrypt signing key seed: %w", err)
		}

		responseTTL := time.Duration(0)
		if userClaims.Resp != nil {
			responseTTL = time.Duration(userClaims.Resp.Expires)
		}

		scopedKey := &entities.ScopedSigningKey{
			ID:              keyID,
			AccountID:       accountID,
			Name:            userName + "-permissions",
			Description:     "Auto-created from NSC import for user " + userName,
			EncryptedSeed:   encryptedSigningSeed,
			PublicKey:       signingKeyPubKey,
			PubAllow:        userClaims.Pub.Allow,
			PubDeny:         userClaims.Pub.Deny,
			SubAllow:        userClaims.Sub.Allow,
			SubDeny:         userClaims.Sub.Deny,
			ResponseMaxMsgs: 0,
			ResponseTTL:     responseTTL,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}

		if userClaims.Resp != nil {
			scopedKey.ResponseMaxMsgs = userClaims.Resp.MaxMsgs
		}

		if err := s.scopedKeyRepo.Create(ctx, scopedKey); err != nil {
			return fmt.Errorf("failed to create scoped signing key: %w", err)
		}
	}

	// Create user entity
	userID := uuid.New()
	user := &entities.User{
		ID:                 userID,
		AccountID:          accountID,
		Name:               userClaims.Name,
		Description:        description,
		EncryptedSeed:      encryptedSeed,
		PublicKey:          userPubKey,
		ScopedSigningKeyID: scopedKeyID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// Use the original user JWT from NSC (don't re-sign it)
	// This preserves the original signature and scoped signing key relationships
	user.JWT = string(userJWTData)

	// Save user
	if err := s.userRepo.Create(ctx, user); err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}
