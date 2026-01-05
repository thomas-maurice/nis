package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
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
		EncryptedCreds:      "", // No automatic credentials
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	// Save to repository
	if err := s.repo.Create(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to create cluster: %w", err)
	}

	return cluster, nil
}

// createClusterUser creates a user in the SYS account for cluster management
func (s *ClusterService) createClusterUser(ctx context.Context, clusterName string, sysAccount *entities.Account) (*entities.User, error) {
	// Generate a unique name for the cluster management user
	userName := fmt.Sprintf("cluster-%s", clusterName)

	// Check if user already exists (in case of retry)
	existing, err := s.userRepo.GetByName(ctx, sysAccount.ID, userName)
	if err == nil && existing != nil {
		// User already exists, return it
		return existing, nil
	}
	if err != nil && err != repositories.ErrNotFound {
		return nil, fmt.Errorf("failed to check existing user: %w", err)
	}

	// Generate user NKey pair
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user keys: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt user seed: %w", err)
	}

	// Create user entity
	user := &entities.User{
		ID:            uuid.New(),
		AccountID:     sysAccount.ID,
		Name:          userName,
		Description:   fmt.Sprintf("Management user for cluster %s", clusterName),
		EncryptedSeed: encryptedSeed,
		PublicKey:     pubKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Generate JWT signed by the system account
	jwt, err := s.jwtService.GenerateUserJWT(ctx, user, sysAccount, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user JWT: %w", err)
	}
	user.JWT = jwt

	// Save to repository
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return user, nil
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

// SyncCluster pushes all account JWTs for the operator to the NATS cluster resolver
// It re-signs all accounts and users before pushing them to ensure fresh JWTs
func (s *ClusterService) SyncCluster(ctx context.Context, id uuid.UUID) ([]string, error) {
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

	// Get operator for signing
	operator, err := s.operatorRepo.GetByID(ctx, cluster.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Get all accounts for this operator
	accounts, err := s.accountRepo.ListByOperator(ctx, cluster.OperatorID, repositories.ListOptions{
		Limit:  1000, // TODO: Handle pagination if needed
		Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	if len(accounts) == 0 {
		return []string{}, nil
	}

	// Re-sign all accounts and their users
	for _, account := range accounts {
		// Re-sign account JWT
		newAccountJWT, err := s.jwtService.GenerateAccountJWT(ctx, account, operator)
		if err != nil {
			return nil, fmt.Errorf("failed to re-sign account %s: %w", account.Name, err)
		}
		account.JWT = newAccountJWT

		// Update account in database
		if err := s.accountRepo.Update(ctx, account); err != nil {
			return nil, fmt.Errorf("failed to update account %s: %w", account.Name, err)
		}

		// Get all users for this account and re-sign them
		users, err := s.userRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{
			Limit:  1000, // TODO: Handle pagination if needed
			Offset: 0,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list users for account %s: %w", account.Name, err)
		}

		// Re-sign each user
		for _, user := range users {
			// Determine if user has a scoped signing key
			var scopedKey *entities.ScopedSigningKey
			if user.ScopedSigningKeyID != nil {
				scopedKey, err = s.scopedKeyRepo.GetByID(ctx, *user.ScopedSigningKeyID)
				if err != nil {
					return nil, fmt.Errorf("failed to get scoped key for user %s: %w", user.Name, err)
				}
			}

			// Re-sign user JWT
			newUserJWT, err := s.jwtService.GenerateUserJWT(ctx, user, account, scopedKey)
			if err != nil {
				return nil, fmt.Errorf("failed to re-sign user %s: %w", user.Name, err)
			}
			user.JWT = newUserJWT

			// Update user in database
			if err := s.userRepo.Update(ctx, user); err != nil {
				return nil, fmt.Errorf("failed to update user %s: %w", user.Name, err)
			}
		}
	}

	// Connect to NATS using cluster credentials
	natsClient, err := s.connectToCluster(cluster.ServerURLs, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS cluster: %w", err)
	}
	defer natsClient.Close()

	// Push each account JWT to the resolver (now with freshly re-signed JWTs)
	accountNames := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.JWT == "" {
			// Skip accounts without JWTs (shouldn't happen, but be defensive)
			continue
		}

		if err := natsClient.PushAccountJWT(ctx, account); err != nil {
			return nil, fmt.Errorf("failed to push JWT for account %s: %w", account.Name, err)
		}

		accountNames = append(accountNames, account.Name)
	}

	return accountNames, nil
}

// connectToCluster creates a NATS client connection using credentials
func (s *ClusterService) connectToCluster(serverURLs []string, creds string) (*nats.Client, error) {
	// Use the NATS client from infrastructure package
	// It now supports credentials from content directly
	return nats.NewClientFromCreds(serverURLs, creds)
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
			natsClient, err := s.connectToCluster(cluster.ServerURLs, creds)
			if err != nil {
				healthErr = fmt.Sprintf("failed to connect: %v", err)
			} else {
				// Successfully connected
				healthy = true
				natsClient.Close()
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
