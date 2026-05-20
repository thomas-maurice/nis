package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ImportFromNSC imports an operator from an NSC archive.
// The archive should contain the NSC store structure:
//
//	operator/operator.jwt
//	operator/accounts/{accountName}/{accountName}.jwt
//	operator/accounts/{accountName}/users/{userName}.jwt
//	nkeys/keys/{type}/{prefix}/{fullkey}.nk
//
// All database writes happen inside a single factory.WithTx, so a mid-import
// failure rolls back the entire operator tree — no orphan accounts pointing
// at a half-created operator, no users left over after a downstream error.
// Archive extraction stays OUTSIDE the tx (filesystem state can't be rolled
// back; the temp directory is cleaned by defer regardless of outcome).
func (s *ExportService) ImportFromNSC(ctx context.Context, archiveData []byte, operatorName string) (uuid.UUID, error) {
	// Extract archive to temp directory — OS-level work, not transactional.
	tempDir, err := extractArchive(archiveData)
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

	var operatorID uuid.UUID
	err = s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		id, e := s.importFromNSCTx(ctx, tx, nscDir, operatorName)
		operatorID = id
		return e
	})
	if err != nil {
		return uuid.Nil, err
	}
	return operatorID, nil
}

// importFromNSCTx is the transactional body of ImportFromNSC. All DB writes
// flow through the tx-scoped factory passed in by the caller.
func (s *ExportService) importFromNSCTx(ctx context.Context, tx persistence.RepositoryFactory, nscDir, operatorName string) (uuid.UUID, error) {
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
		CreatedAt:     clock.Now(),
		UpdatedAt:     clock.Now(),
	}

	// Save operator via tx-scoped repo
	if err := tx.OperatorRepository().Create(ctx, operator); err != nil {
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
			accountPubKey, err := s.importNSCAccount(ctx, tx, nscDir, operatorID, operatorKeyPair, accountName)
			if err != nil {
				return uuid.Nil, fmt.Errorf("failed to import account %s: %w", accountName, err)
			}

			// Track SYS or $SYS account
			if accountName == "SYS" || accountName == "$SYS" {
				sysAccountPubKey = accountPubKey
			}
		}
	}

	// Set system account if $SYS was found. SetSystemAccountTx uses the same
	// tx-scoped factory so the operator update participates in this tx.
	if sysAccountPubKey != "" {
		if _, err := s.operatorService.SetSystemAccountTx(ctx, tx, operatorID, sysAccountPubKey); err != nil {
			return uuid.Nil, fmt.Errorf("failed to set system account: %w", err)
		}

		// Create system user in $SYS account if it doesn't exist
		if err := s.ensureSystemUser(ctx, tx, operatorID); err != nil {
			return uuid.Nil, fmt.Errorf("failed to ensure system user: %w", err)
		}
	}

	return operatorID, nil
}

// ensureSystemUser creates a system user in the system account if it doesn't exist.
// All reads and writes go through the tx-scoped factory.
func (s *ExportService) ensureSystemUser(ctx context.Context, tx persistence.RepositoryFactory, operatorID uuid.UUID) error {
	operatorRepo := tx.OperatorRepository()
	accountRepo := tx.AccountRepository()
	userRepo := tx.UserRepository()
	scopedKeyRepo := tx.ScopedSigningKeyRepository()

	// Get the operator to find the system account public key
	operator, err := operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}

	if operator.SystemAccountPubKey == "" {
		return fmt.Errorf("operator has no system account configured")
	}

	// Find the system account by matching public key
	accounts, err := accountRepo.ListByOperator(ctx, operatorID, repositories.ListOptions{})
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
	users, err := userRepo.ListByAccount(ctx, sysAccount.ID, repositories.ListOptions{})
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
		scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, sysAccount.ID, repositories.ListOptions{Limit: 1})
		if err == nil && len(scopedKeys) > 0 {
			scopedKeyID = &scopedKeys[0].ID
		}

		userReq := CreateUserRequest{
			AccountID:          sysAccount.ID,
			Name:               "system",
			Description:        "System user for operator management",
			ScopedSigningKeyID: scopedKeyID,
		}

		// Use the tx-aware variant so this single write participates in the
		// outer NSC-import transaction.
		systemUser, err = s.userService.CreateUserTx(ctx, tx, userReq)
		if err != nil {
			return fmt.Errorf("failed to create system user: %w", err)
		}
	}

	// Update all clusters for this operator to use the system user credentials.
	// A fresh NSC import typically has zero clusters at this point, but if any
	// exist, their credential update participates in the same tx.
	if err := s.updateClustersWithSystemUser(ctx, tx, operatorID, systemUser.ID); err != nil {
		// Log but don't fail - credentials can be set later
		// This is not critical for the import to succeed
		logging.LogFromContext(ctx).Warn("failed to update cluster credentials after NSC import",
			"operator_id", operatorID, "error", err)
	}

	return nil
}

// updateClustersWithSystemUser updates all clusters for an operator to use the
// system user. Reads and writes go through the tx-scoped factory.
func (s *ExportService) updateClustersWithSystemUser(ctx context.Context, tx persistence.RepositoryFactory, operatorID, systemUserID uuid.UUID) error {
	clusterRepo := tx.ClusterRepository()

	clusters, err := clusterRepo.ListByOperator(ctx, operatorID, repositories.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	for _, cluster := range clusters {
		_, err := s.clusterService.UpdateClusterCredentialsTx(ctx, tx, cluster.ID, systemUserID)
		if err != nil {
			return fmt.Errorf("failed to update credentials for cluster %s: %w", cluster.Name, err)
		}
	}

	return nil
}

// findNKey locates an nkey file in the NSC store structure.
// NSC stores keys at: nkeys/keys/{type}/{prefix}/{publicKey}.nk
// where type is O (operator), A (account), or U (user)
// and prefix is the first 2 characters after the type prefix.
// Filesystem-only — no DB access, no tx awareness needed.
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

// importNSCAccount imports a single account from NSC and returns the account public key.
// All DB writes flow through the tx-scoped factory passed by the caller.
//
//nolint:unparam // operatorKeyPair retained for parity with NSC import idioms even though we re-sign nothing here today.
func (s *ExportService) importNSCAccount(ctx context.Context, tx persistence.RepositoryFactory, nscDir string, operatorID uuid.UUID, operatorKeyPair nkeys.KeyPair, accountName string) (string, error) {
	_ = operatorKeyPair // reserved for future re-signing — see TODO in ImportFromNSC.

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
		CreatedAt:             clock.Now(),
		UpdatedAt:             clock.Now(),
	}

	// Use the original account JWT from NSC (don't re-sign it).
	// This preserves the original signature and any scoped signing key relationships.
	account.JWT = string(accountJWTData)

	// Save account via tx-scoped repo
	if err := tx.AccountRepository().Create(ctx, account); err != nil {
		return "", fmt.Errorf("failed to create account: %w", err)
	}

	// Get operator for user imports (using the tx-scoped repo so the operator we
	// just created in this tx is visible)
	operator, err := tx.OperatorRepository().GetByID(ctx, operatorID)
	if err != nil {
		return "", fmt.Errorf("failed to get operator: %w", err)
	}

	// Import signing keys from the account JWT. SigningKeys is a
	// map[pubkey]Scope where the value is nil (plain signer — NATS uses
	// the user JWT's own perms) or a *jwt.UserScope (scoped — NATS
	// applies the embedded template, ignoring the user JWT's perms).
	// Forward the scope so importNSCScopedSigningKey can preserve the
	// template's pub/sub/Resp values and flip IsPlainSigner correctly.
	if len(accountClaims.SigningKeys) > 0 {
		for signingKeyPubKey, scope := range accountClaims.SigningKeys {
			if err := s.importNSCScopedSigningKey(ctx, tx, nscDir, accountID, signingKeyPubKey, scope); err != nil {
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
			if err := s.importNSCUser(ctx, tx, nscDir, accountID, accountName, userName, operator); err != nil {
				return "", fmt.Errorf("failed to import user %s: %w", userName, err)
			}
		}
	}

	return accountPubKey, nil
}

// importNSCScopedSigningKey imports a signing key from NSC via the tx.
// `scope` is the value from accountClaims.SigningKeys[pubkey]:
//   - nil  → plain signer; user JWTs we mint must NOT use SetScoped, and
//     the SKK is emitted as a raw string on account-JWT regen.
//   - *jwt.UserScope → scoped signer; copy the embedded Template's
//     pub/sub/Resp into the SKK row so a future account-JWT regen
//     reproduces the same template (preserving the operator's intent).
//     Without this, NIS would later emit the SKK with empty perms and
//     existing users that rely on the original template restrictions
//     would silently lose those restrictions.
func (s *ExportService) importNSCScopedSigningKey(ctx context.Context, tx persistence.RepositoryFactory, nscDir string, accountID uuid.UUID, signingKeyPubKey string, scope jwt.Scope) error {
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

	scopedKeyID := uuid.New()
	// Defaults: assume plain signer with empty perms. Overridden below
	// if the source NSC archive listed this key as a UserScope.
	scopedKey := &entities.ScopedSigningKey{
		ID:            scopedKeyID,
		AccountID:     accountID,
		Name:          fmt.Sprintf("imported-key-%s", signingKeyPubKey[1:5]),
		Description:   "Scoped signing key imported from NSC",
		EncryptedSeed: encryptedSeed,
		PublicKey:     signingKeyPubKey,
		PubAllow:      []string{},
		PubDeny:       []string{},
		SubAllow:      []string{},
		SubDeny:       []string{},
		IsPlainSigner: true,
		CreatedAt:     clock.Now(),
		UpdatedAt:     clock.Now(),
	}
	if us, ok := scope.(*jwt.UserScope); ok && us != nil {
		// Preserve the template's permission surface so a future
		// account-JWT regen reproduces the same scope. NatsLimits
		// from the template are NOT mirrored onto the SKK row —
		// jwt_service.NewUserScope re-defaults them to NoLimit on
		// regen and our SKK schema doesn't have per-key NatsLimits
		// columns. If an operator ever needs per-SKK NatsLimits
		// after import, that's a separate schema extension.
		scopedKey.IsPlainSigner = false
		scopedKey.PubAllow = append([]string{}, us.Template.Pub.Allow...)
		scopedKey.PubDeny = append([]string{}, us.Template.Pub.Deny...)
		scopedKey.SubAllow = append([]string{}, us.Template.Sub.Allow...)
		scopedKey.SubDeny = append([]string{}, us.Template.Sub.Deny...)
		if us.Template.Resp != nil {
			scopedKey.ResponseMaxMsgs = us.Template.Resp.MaxMsgs
			scopedKey.ResponseTTL = us.Template.Resp.Expires
		}
		// Surface the role from the NSC scope so it's discoverable
		// in the UI/CLI; fall back to the auto-name if NSC didn't
		// set one (rare — `nsc edit signing-key` usually does).
		if us.Role != "" {
			scopedKey.Name = us.Role
		}
		if us.Description != "" {
			scopedKey.Description = us.Description
		}
	}

	// Save via tx-scoped repo
	if err := tx.ScopedSigningKeyRepository().Create(ctx, scopedKey); err != nil {
		return fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	return nil
}

// importNSCUser imports a single user from NSC via the tx.
// Note: NSC users store their permissions in the JWT, but NIS users get permissions
// from scoped signing keys. We create the user without a scoped key and let the
// user service generate the JWT with default permissions from the account.
//
//nolint:unparam // operator retained for parity with future re-signing logic.
func (s *ExportService) importNSCUser(ctx context.Context, tx persistence.RepositoryFactory, nscDir string, accountID uuid.UUID, accountName string, userName string, operator *entities.Operator) error {
	_ = operator // reserved for future re-signing — see TODO in ImportFromNSC.

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
			CreatedAt:       clock.Now(),
			UpdatedAt:       clock.Now(),
		}

		if userClaims.Resp != nil {
			scopedKey.ResponseMaxMsgs = userClaims.Resp.MaxMsgs
		}

		if err := tx.ScopedSigningKeyRepository().Create(ctx, scopedKey); err != nil {
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
		CreatedAt:          clock.Now(),
		UpdatedAt:          clock.Now(),
	}

	// Use the original user JWT from NSC (don't re-sign it).
	// This preserves the original signature and scoped signing key relationships.
	user.JWT = string(userJWTData)

	// Save via tx-scoped repo
	if err := tx.UserRepository().Create(ctx, user); err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}
