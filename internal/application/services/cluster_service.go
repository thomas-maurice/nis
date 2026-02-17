package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/nats"
)

// ClusterService provides business logic for cluster management
type ClusterService struct {
	repo          repositories.ClusterRepository
	operatorRepo  repositories.OperatorRepository
	accountRepo   repositories.AccountRepository
	userRepo      repositories.UserRepository
	scopedKeyRepo repositories.ScopedSigningKeyRepository
	encryptor     encryption.Encryptor
	jwtService    *JWTService
}

// NewClusterService creates a new cluster service
func NewClusterService(
	repo repositories.ClusterRepository,
	operatorRepo repositories.OperatorRepository,
	accountRepo repositories.AccountRepository,
	userRepo repositories.UserRepository,
	scopedKeyRepo repositories.ScopedSigningKeyRepository,
	encryptor encryption.Encryptor,
	jwtService *JWTService,
) *ClusterService {
	return &ClusterService{
		repo:          repo,
		operatorRepo:  operatorRepo,
		accountRepo:   accountRepo,
		userRepo:      userRepo,
		scopedKeyRepo: scopedKeyRepo,
		encryptor:     encryptor,
		jwtService:    jwtService,
	}
}

// CreateClusterRequest contains the data needed to create a cluster
type CreateClusterRequest struct {
	Name                string
	Description         string
	ServerURLs          []string
	OperatorID          uuid.UUID
	SystemAccountPubKey string   // Optional
	SystemAccountUserID *uuid.UUID // Optional - if provided, generates encrypted creds
	SkipVerifyTLS       bool
}

// CreateCluster creates a new cluster configuration and automatically creates a SYS user for management
func (s *ClusterService) CreateCluster(ctx context.Context, req CreateClusterRequest) (*entities.Cluster, error) {
	// Validate request
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if len(req.ServerURLs) == 0 {
		return nil, fmt.Errorf("at least one server URL is required")
	}

	// Get operator to verify it exists and has system account
	operator, err := s.operatorRepo.GetByID(ctx, req.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Verify operator has a system account configured
	if operator.SystemAccountPubKey == "" {
		return nil, fmt.Errorf("operator does not have a system account configured")
	}

	// Check if cluster with this name already exists
	existing, err := s.repo.GetByName(ctx, req.Name)
	if err != nil && err != repositories.ErrNotFound {
		return nil, fmt.Errorf("failed to check existing cluster: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// Get the system account by its public key
	sysAccount, err := s.accountRepo.GetByPublicKey(ctx, operator.SystemAccountPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get system account: %w", err)
	}

	// Create cluster entity
	cluster := &entities.Cluster{
		ID:                  uuid.New(),
		Name:                req.Name,
		Description:         req.Description,
		ServerURLs:          req.ServerURLs,
		OperatorID:          req.OperatorID,
		SystemAccountPubKey: sysAccount.PublicKey,
		EncryptedCreds:      "", // Will be set below if system user exists
		SkipVerifyTLS:       req.SkipVerifyTLS,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	// Save to repository
	if err := s.repo.Create(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to create cluster: %w", err)
	}

	// Try to automatically set credentials using system user if it exists
	users, err := s.userRepo.ListByAccount(ctx, sysAccount.ID, repositories.ListOptions{})
	if err == nil && len(users) > 0 {
		// Look for user named "system"
		var systemUser *entities.User
		for i := range users {
			if users[i].Name == "system" {
				systemUser = users[i]
				break
			}
		}

		if systemUser != nil {
			// Set cluster credentials
			_, err := s.UpdateClusterCredentials(ctx, cluster.ID, systemUser.ID)
			if err != nil {
				// Log but don't fail cluster creation if credentials can't be set
				// Credentials can be set manually later
				fmt.Printf("Warning: failed to set automatic cluster credentials: %v\n", err)
			}
		}
	}

	return cluster, nil
}

// GetCluster retrieves a cluster by ID
func (s *ClusterService) GetCluster(ctx context.Context, id uuid.UUID) (*entities.Cluster, error) {
	return s.repo.GetByID(ctx, id)
}

// GetClusterByName retrieves a cluster by name
func (s *ClusterService) GetClusterByName(ctx context.Context, name string) (*entities.Cluster, error) {
	return s.repo.GetByName(ctx, name)
}

// ListClusters retrieves all clusters with pagination
func (s *ClusterService) ListClusters(ctx context.Context, opts repositories.ListOptions) ([]*entities.Cluster, error) {
	return s.repo.List(ctx, opts)
}

// ListAllClusters lists all clusters across all operators
func (s *ClusterService) ListAllClusters(ctx context.Context, opts repositories.ListOptions) ([]*entities.Cluster, error) {
	return s.repo.List(ctx, opts)
}

// ListClustersByOperator retrieves all clusters for an operator with pagination
func (s *ClusterService) ListClustersByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Cluster, error) {
	return s.repo.ListByOperator(ctx, operatorID, opts)
}

// UpdateClusterRequest contains the fields that can be updated
type UpdateClusterRequest struct {
	Name                *string
	Description         *string
	ServerURLs          []string
	SystemAccountPubKey *string
	SkipVerifyTLS       *bool
}

// UpdateCluster updates a cluster's configuration
func (s *ClusterService) UpdateCluster(ctx context.Context, id uuid.UUID, req UpdateClusterRequest) (*entities.Cluster, error) {
	// Get existing cluster
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	updated := false
	if req.Name != nil && *req.Name != cluster.Name {
		// Check if new name is already taken
		existing, err := s.repo.GetByName(ctx, *req.Name)
		if err != nil && err != repositories.ErrNotFound {
			return nil, fmt.Errorf("failed to check existing cluster: %w", err)
		}
		if existing != nil && existing.ID != id {
			return nil, repositories.ErrAlreadyExists
		}
		cluster.Name = *req.Name
		updated = true
	}

	if req.Description != nil && *req.Description != cluster.Description {
		cluster.Description = *req.Description
		updated = true
	}

	if req.ServerURLs != nil {
		cluster.ServerURLs = req.ServerURLs
		updated = true
	}

	if req.SystemAccountPubKey != nil && *req.SystemAccountPubKey != cluster.SystemAccountPubKey {
		cluster.SystemAccountPubKey = *req.SystemAccountPubKey
		updated = true
	}

	if req.SkipVerifyTLS != nil && *req.SkipVerifyTLS != cluster.SkipVerifyTLS {
		cluster.SkipVerifyTLS = *req.SkipVerifyTLS
		updated = true
	}

	if !updated {
		return cluster, nil
	}

	cluster.UpdatedAt = time.Now()

	// Save changes
	if err := s.repo.Update(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to update cluster: %w", err)
	}

	return cluster, nil
}

// UpdateClusterCredentials updates the encrypted system account credentials
func (s *ClusterService) UpdateClusterCredentials(ctx context.Context, id uuid.UUID, systemAccountUserID uuid.UUID) (*entities.Cluster, error) {
	// Get existing cluster
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Get system account user
	user, err := s.userRepo.GetByID(ctx, systemAccountUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get system account user: %w", err)
	}

	// Generate credentials
	creds, err := s.jwtService.GetUserCredentials(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("failed to get user credentials: %w", err)
	}

	// Encrypt credentials
	encryptedCreds, err := s.encryptor.Encrypt(ctx, []byte(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt credentials: %w", err)
	}

	cluster.EncryptedCreds = encryptedCreds
	cluster.UpdatedAt = time.Now()

	// Save changes
	if err := s.repo.Update(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to update cluster: %w", err)
	}

	return cluster, nil
}

// GetClusterCredentials retrieves and decrypts the system account credentials
func (s *ClusterService) GetClusterCredentials(ctx context.Context, id uuid.UUID) (string, error) {
	// Get cluster
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", err
	}

	if cluster.EncryptedCreds == "" {
		return "", fmt.Errorf("cluster has no system account credentials configured")
	}

	// Decrypt credentials
	credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credentials: %w", err)
	}

	return string(credsBytes), nil
}

// DeleteCluster deletes a cluster
func (s *ClusterService) DeleteCluster(ctx context.Context, id uuid.UUID) error {
	// Check if cluster exists
	_, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Delete cluster
	return s.repo.Delete(ctx, id)
}

// SyncResult contains the result of a sync operation
type SyncResult struct {
	Accounts        []string
	AccountsAdded   int
	AccountsRemoved int
	AccountsUpdated int
	RemovedAccounts []string
	Errors          []SyncError
}

// SyncError represents an error encountered during sync
type SyncError struct {
	AccountPublicKey string
	AccountName      string
	Error            string
}

// SyncCluster pushes all account JWTs for the operator to the NATS cluster resolver
// If prune is true, it also removes accounts from the resolver that are not in the database
func (s *ClusterService) SyncCluster(ctx context.Context, id uuid.UUID, prune bool) (*SyncResult, error) {
	// Get cluster
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	if cluster.EncryptedCreds == "" {
		return nil, fmt.Errorf("cluster has no system account credentials configured")
	}

	// Decrypt credentials
	credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
	}
	creds := string(credsBytes)

	// Get all accounts for this operator
	accounts, err := s.accountRepo.ListByOperator(ctx, cluster.OperatorID, repositories.ListOptions{
		Limit:  1000, // TODO: Handle pagination if needed
		Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	// Connect to NATS using cluster credentials
	natsClient, err := s.connectToCluster(cluster.ServerURLs, creds, cluster.SkipVerifyTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS cluster: %w", err)
	}
	defer func() { _ = natsClient.Close() }()

	result := &SyncResult{
		Accounts:        make([]string, 0),
		RemovedAccounts: make([]string, 0),
		Errors:          make([]SyncError, 0),
	}

	// Build a map of database accounts by public key
	dbAccountsByPubKey := make(map[string]*entities.Account)
	for _, account := range accounts {
		dbAccountsByPubKey[account.PublicKey] = account
	}

	// Get list of accounts currently on the resolver
	var resolverAccounts []string
	if prune {
		resolverAccounts, err = natsClient.ListAccountsFromResolver(ctx)
		if err != nil {
			// Log error but continue with sync - pruning just won't happen
			result.Errors = append(result.Errors, SyncError{
				Error: fmt.Sprintf("failed to list resolver accounts: %v", err),
			})
		}
	}

	// Push each account JWT to the resolver
	for _, account := range accounts {
		if account.JWT == "" {
			// Skip accounts without JWTs (shouldn't happen, but be defensive)
			continue
		}

		if err := natsClient.PushAccountJWT(ctx, account); err != nil {
			result.Errors = append(result.Errors, SyncError{
				AccountPublicKey: account.PublicKey,
				AccountName:      account.Name,
				Error:            fmt.Sprintf("failed to push JWT: %v", err),
			})
			continue
		}

		result.Accounts = append(result.Accounts, account.Name)
		result.AccountsUpdated++
	}

	// Prune stale accounts from resolver if requested
	if prune && len(resolverAccounts) > 0 {
		// Collect stale accounts to delete
		var staleAccounts []string
		for _, resolverPubKey := range resolverAccounts {
			// Skip if account exists in database
			if _, exists := dbAccountsByPubKey[resolverPubKey]; exists {
				continue
			}

			// Skip system account - never delete it
			if resolverPubKey == cluster.SystemAccountPubKey {
				continue
			}

			staleAccounts = append(staleAccounts, resolverPubKey)
		}

		// Delete stale accounts if any
		if len(staleAccounts) > 0 {
			// Get the operator to sign the delete claim
			operator, err := s.operatorRepo.GetByID(ctx, cluster.OperatorID)
			if err != nil {
				result.Errors = append(result.Errors, SyncError{
					Error: fmt.Sprintf("failed to get operator for delete claim: %v", err),
				})
			} else {
				// Generate operator-signed delete claim JWT
				deleteClaimJWT, err := s.jwtService.GenerateDeleteClaimJWT(ctx, operator, staleAccounts)
				if err != nil {
					result.Errors = append(result.Errors, SyncError{
						Error: fmt.Sprintf("failed to generate delete claim JWT: %v", err),
					})
				} else {
					// Delete the stale accounts
					if err := natsClient.DeleteAccountJWT(ctx, deleteClaimJWT); err != nil {
						result.Errors = append(result.Errors, SyncError{
							Error: fmt.Sprintf("failed to delete stale accounts: %v", err),
						})
					} else {
						// Mark all stale accounts as removed
						for _, pubKey := range staleAccounts {
							result.RemovedAccounts = append(result.RemovedAccounts, pubKey)
							result.AccountsRemoved++
						}
					}
				}
			}
		}
	}

	return result, nil
}

// ListResolverAccounts lists all account public keys currently on the NATS resolver
func (s *ClusterService) ListResolverAccounts(ctx context.Context, clusterID uuid.UUID) ([]string, error) {
	// Get cluster
	cluster, err := s.repo.GetByID(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	if cluster.EncryptedCreds == "" {
		return nil, fmt.Errorf("cluster has no system account credentials configured")
	}

	// Decrypt credentials
	credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
	}
	creds := string(credsBytes)

	// Connect to NATS using cluster credentials
	natsClient, err := s.connectToCluster(cluster.ServerURLs, creds, cluster.SkipVerifyTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS cluster: %w", err)
	}
	defer func() { _ = natsClient.Close() }()

	// List accounts from resolver
	return natsClient.ListAccountsFromResolver(ctx)
}

// DeleteResolverAccount removes an account from the NATS resolver
func (s *ClusterService) DeleteResolverAccount(ctx context.Context, clusterID uuid.UUID, publicKey string) error {
	// Get cluster
	cluster, err := s.repo.GetByID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	if cluster.EncryptedCreds == "" {
		return fmt.Errorf("cluster has no system account credentials configured")
	}

	// Safety check: don't allow deleting the system account
	if publicKey == cluster.SystemAccountPubKey {
		return fmt.Errorf("cannot delete system account from resolver")
	}

	// Decrypt credentials
	credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		return fmt.Errorf("failed to decrypt credentials: %w", err)
	}
	creds := string(credsBytes)

	// Connect to NATS using cluster credentials
	natsClient, err := s.connectToCluster(cluster.ServerURLs, creds, cluster.SkipVerifyTLS)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS cluster: %w", err)
	}
	defer func() { _ = natsClient.Close() }()

	// Get the operator to sign the delete claim
	operator, err := s.operatorRepo.GetByID(ctx, cluster.OperatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}

	// Generate operator-signed delete claim JWT
	deleteClaimJWT, err := s.jwtService.GenerateDeleteClaimJWT(ctx, operator, []string{publicKey})
	if err != nil {
		return fmt.Errorf("failed to generate delete claim JWT: %w", err)
	}

	// Delete account from resolver
	return natsClient.DeleteAccountJWT(ctx, deleteClaimJWT)
}

// connectToCluster creates a NATS client connection using credentials
func (s *ClusterService) connectToCluster(serverURLs []string, creds string, skipVerifyTLS bool) (*nats.Client, error) {
	// Use the NATS client from infrastructure package
	// It now supports credentials from content directly
	return nats.NewClientFromCreds(serverURLs, creds, skipVerifyTLS)
}

// CheckClusterHealth checks if a cluster is reachable and updates its health status
func (s *ClusterService) CheckClusterHealth(ctx context.Context, id uuid.UUID) error {
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	healthy := false
	var healthErr string
	now := time.Now()

	// Try to connect to the cluster
	if cluster.EncryptedCreds != "" {
		credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
		if err != nil {
			healthErr = fmt.Sprintf("failed to decrypt credentials: %v", err)
		} else {
			creds := string(credsBytes)
			natsClient, err := s.connectToCluster(cluster.ServerURLs, creds, cluster.SkipVerifyTLS)
			if err != nil {
				healthErr = fmt.Sprintf("failed to connect: %v", err)
			} else {
				// Successfully connected
				healthy = true
				_ = natsClient.Close()
			}
		}
	} else {
		healthErr = "no credentials configured"
	}

	// Update health status
	cluster.Healthy = healthy
	cluster.LastHealthCheck = &now
	cluster.HealthCheckError = healthErr
	cluster.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to update cluster health status: %w", err)
	}

	return nil
}

// CheckAllClustersHealth checks health for all clusters
func (s *ClusterService) CheckAllClustersHealth(ctx context.Context) error {
	clusters, err := s.repo.List(ctx, repositories.ListOptions{
		Limit:  1000, // TODO: Handle pagination
		Offset: 0,
	})
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	for _, cluster := range clusters {
		// Check each cluster's health (ignore errors for individual clusters)
		_ = s.CheckClusterHealth(ctx, cluster.ID)
	}

	return nil
}
