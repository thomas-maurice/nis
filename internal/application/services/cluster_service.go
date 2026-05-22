package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/nats"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
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
	factory       persistence.RepositoryFactory // optional; set via WithFactory for event emission
	jobRunner     *JobRunner                    // optional; set via WithJobRunner for eager cluster.health_check enqueue on CreateCluster
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

// WithFactory attaches a repository factory to the service, enabling event emission.
// Call this from serve.go after constructing the service. Tests that don't call this
// will skip event emission (factory is nil).
func (s *ClusterService) WithFactory(f persistence.RepositoryFactory) *ClusterService {
	s.factory = f
	return s
}

// WithJobRunner attaches the jobs substrate to the service so CreateCluster
// can enqueue an immediate cluster.health_check job. Without this wiring the
// new cluster waits up to one sweep interval (default 60s) for its first
// probe, which makes UI/CLI flows that depend on Healthy momentarily lie.
// Tests that don't call this will silently skip the eager enqueue.
func (s *ClusterService) WithJobRunner(r *JobRunner) *ClusterService {
	s.jobRunner = r
	return s
}

// enqueueImmediateHealthCheck schedules a one-shot cluster.health_check for
// a freshly-created cluster. Dedup_key matches the sweep handler's, so a
// concurrent sweep trying to enqueue for the same cluster is a no-op.
// Failures are logged, not propagated — the next sweep tick catches up.
func (s *ClusterService) enqueueImmediateHealthCheck(ctx context.Context, clusterID uuid.UUID) {
	if s.jobRunner == nil {
		return
	}
	payload := ClusterHealthCheckPayload{ClusterID: clusterID.String()}
	if _, err := s.jobRunner.EnsureScheduled(
		ctx,
		JobTypeClusterHealthCheck,
		payload,
		clock.Now(),
		clusterHealthCheckDedupKey(clusterID),
	); err != nil {
		logging.LogFromContext(ctx).Warn("cluster create: eager health-check enqueue failed",
			"cluster_id", clusterID, "error", err)
	}
}

// CreateClusterRequest contains the data needed to create a cluster
type CreateClusterRequest struct {
	Name                string
	Description         string
	ServerURLs          []string
	OperatorID          uuid.UUID
	SystemAccountPubKey string     // Optional
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
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
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
		CreatedAt:           clock.Now(),
		UpdatedAt:           clock.Now(),
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
				logging.LogFromContext(ctx).Warn("failed to set automatic cluster credentials",
					"cluster", cluster.Name, "error", err)
			}
		}
	}

	if s.factory != nil {
		if err := events.EmitSystem(ctx, s.factory, events.Event{
			Type:         entities.EventTypeClusterCreated,
			OperatorID:   &cluster.OperatorID,
			ResourceType: "cluster",
			ResourceID:   cluster.ID.String(),
			Payload:      map[string]any{"name": cluster.Name, "urls": cluster.ServerURLs},
		}); err != nil {
			return nil, fmt.Errorf("emit cluster.created: %w", err)
		}
	}

	s.enqueueImmediateHealthCheck(ctx, cluster.ID)

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

// ListClustersByOperator retrieves all clusters for an operator with pagination
func (s *ClusterService) ListClustersByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Cluster, error) {
	return s.repo.ListByOperator(ctx, operatorID, opts)
}

// ListClustersPage returns one keyset-paginated page of clusters visible
// under scope. Tenant scoping is enforced at the repo via SQL WHERE — no
// post-fetch filter here. See package authz and SKILL §15.
func (s *ClusterService) ListClustersPage(ctx context.Context, scope authz.Scope, filter repositories.ClusterListFilter) ([]*entities.Cluster, string, error) {
	return s.repo.ListPage(ctx, scope, filter)
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
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
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

	cluster.UpdatedAt = clock.Now()

	// Save changes
	if err := s.repo.Update(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to update cluster: %w", err)
	}

	if s.factory != nil {
		if err := events.EmitSystem(ctx, s.factory, events.Event{
			Type:         entities.EventTypeClusterUpdated,
			OperatorID:   &cluster.OperatorID,
			ResourceType: "cluster",
			ResourceID:   cluster.ID.String(),
			Payload:      map[string]any{"name": cluster.Name},
		}); err != nil {
			return nil, fmt.Errorf("emit cluster.updated: %w", err)
		}
	}

	return cluster, nil
}

// UpdateClusterCredentials updates the encrypted system account credentials
func (s *ClusterService) UpdateClusterCredentials(ctx context.Context, id uuid.UUID, systemAccountUserID uuid.UUID) (*entities.Cluster, error) {
	return s.updateClusterCredentialsWith(ctx, s.repo, s.userRepo, id, systemAccountUserID)
}

// UpdateClusterCredentialsTx is the tx-aware variant. Callers inside another
// service's factory.WithTx pass the tx-scoped factory so the cluster update
// participates in the surrounding rollback boundary. Used by
// ExportService.ImportFromNSC.
func (s *ClusterService) UpdateClusterCredentialsTx(ctx context.Context, tx persistence.RepositoryFactory, id uuid.UUID, systemAccountUserID uuid.UUID) (*entities.Cluster, error) {
	cluster, err := s.updateClusterCredentialsWith(ctx, tx.ClusterRepository(), tx.UserRepository(), id, systemAccountUserID)
	if err != nil {
		return nil, err
	}
	if err := events.EmitTx(ctx, tx, events.Event{
		Type:         entities.EventTypeClusterUpdated,
		OperatorID:   &cluster.OperatorID,
		ResourceType: "cluster",
		ResourceID:   cluster.ID.String(),
		Payload:      map[string]any{"name": cluster.Name, "changed": []string{"credentials"}},
	}); err != nil {
		return nil, fmt.Errorf("emit cluster.updated: %w", err)
	}
	return cluster, nil
}

func (s *ClusterService) updateClusterCredentialsWith(ctx context.Context, clusterRepo repositories.ClusterRepository, userRepo repositories.UserRepository, id uuid.UUID, systemAccountUserID uuid.UUID) (*entities.Cluster, error) {
	// Get existing cluster
	cluster, err := clusterRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Get system account user
	user, err := userRepo.GetByID(ctx, systemAccountUserID)
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
	cluster.UpdatedAt = clock.Now()

	// Save changes
	if err := clusterRepo.Update(ctx, cluster); err != nil {
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
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Delete cluster
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	if s.factory != nil {
		if err := events.EmitSystem(ctx, s.factory, events.Event{
			Type:         entities.EventTypeClusterDeleted,
			OperatorID:   &cluster.OperatorID,
			ResourceType: "cluster",
			ResourceID:   cluster.ID.String(),
			Payload:      map[string]any{"name": cluster.Name},
		}); err != nil {
			return fmt.Errorf("emit cluster.deleted: %w", err)
		}
	}

	return nil
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

// DeleteAccountFromAllClusters removes a single account's JWT from every
// cluster owned by the operator by sending an operator-signed delete-claim to
// each. Mirrors the prune branch of SyncCluster but scoped to one pubkey.
//
// Returns one error per cluster that refused the delete; a single failure
// does NOT short-circuit the others. The caller is expected to log them —
// DB-side delete already succeeded and we don't want to roll it back over a
// transient NATS hiccup (the resolver is reconciled best-effort, DB is the
// source of truth — same semantic as PushAccountToAllClusters).
//
// `accountPublicKey` is taken explicitly rather than re-reading the account
// row because the typical caller (AccountService.DeleteAccount) has already
// removed the row from the DB by the time this runs.
func (s *ClusterService) DeleteAccountFromAllClusters(ctx context.Context, operatorID uuid.UUID, accountPublicKey string) []SyncError {
	if accountPublicKey == "" {
		return nil
	}

	clusters, err := s.repo.ListByOperator(ctx, operatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return []SyncError{{Error: fmt.Sprintf("list clusters for operator %s: %v", operatorID, err)}}
	}
	if len(clusters) == 0 {
		return nil
	}

	// The delete-claim is operator-signed and identical across clusters —
	// load + sign once, send to each. Saves N redundant JWT signs in a
	// many-cluster operator.
	operator, err := s.operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return []SyncError{{Error: fmt.Sprintf("get operator %s: %v", operatorID, err)}}
	}
	deleteClaim, err := s.jwtService.GenerateDeleteClaimJWT(ctx, operator, []string{accountPublicKey})
	if err != nil {
		return []SyncError{{Error: fmt.Sprintf("generate delete claim for %s: %v", accountPublicKey, err)}}
	}

	var errs []SyncError
	for _, cluster := range clusters {
		if cluster.EncryptedCreds == "" {
			// No creds → nothing to push to. Skip silently, same shape as
			// PushAccountToAllClusters.
			continue
		}
		natsClient, _, openErr := s.openManagedCluster(ctx, cluster.ID)
		if openErr != nil {
			errs = append(errs, SyncError{
				AccountPublicKey: accountPublicKey,
				Error:            fmt.Sprintf("open cluster %s: %v", cluster.Name, openErr),
			})
			continue
		}
		delErr := natsClient.DeleteAccountJWT(ctx, deleteClaim)
		_ = natsClient.Close()
		if delErr != nil {
			errs = append(errs, SyncError{
				AccountPublicKey: accountPublicKey,
				Error:            fmt.Sprintf("delete on cluster %s: %v", cluster.Name, delErr),
			})
		}
	}
	return errs
}

// PushAccountToAllClusters pushes a single account's current JWT to every
// cluster owned by the operator. Used by P2 revocation / prune paths and by
// auto-renew: those operations re-sign the parent account JWT but don't want
// to pay the cost of a full SyncCluster (which iterates every account).
//
// Returns one error per failed cluster; the caller is responsible for surfacing
// them. A single cluster failure does NOT short-circuit the others.
func (s *ClusterService) PushAccountToAllClusters(ctx context.Context, operatorID uuid.UUID, account *entities.Account) []SyncError {
	if account == nil || account.JWT == "" {
		return nil
	}
	clusters, err := s.repo.ListByOperator(ctx, operatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return []SyncError{{Error: fmt.Sprintf("list clusters for operator %s: %v", operatorID, err)}}
	}
	var errs []SyncError
	for _, cluster := range clusters {
		if cluster.EncryptedCreds == "" {
			// Cluster has no creds yet — skip silently; nothing to push to.
			continue
		}
		natsClient, _, openErr := s.openManagedCluster(ctx, cluster.ID)
		if openErr != nil {
			errs = append(errs, SyncError{
				AccountPublicKey: account.PublicKey,
				AccountName:      account.Name,
				Error:            fmt.Sprintf("open cluster %s: %v", cluster.Name, openErr),
			})
			continue
		}
		pushErr := natsClient.PushAccountJWT(ctx, account)
		_ = natsClient.Close()
		if pushErr != nil {
			errs = append(errs, SyncError{
				AccountPublicKey: account.PublicKey,
				AccountName:      account.Name,
				Error:            fmt.Sprintf("push to cluster %s: %v", cluster.Name, pushErr),
			})
		}
	}
	return errs
}

// ClusterPushOutcome carries a per-cluster outcome for PushAccountToAllClustersDetailed.
// OK=true means the account JWT was successfully pushed to that cluster. OK=false
// + non-empty ErrorMessage means the push attempt failed (cluster open, decrypt,
// or NATS publish error). OK=false + empty ErrorMessage means the push was skipped
// (cluster has no system credentials configured). The DB is the source of truth;
// callers surface these outcomes so operators see partial-success state in the
// triggering RPC's response (rather than only in the drift dashboard).
type ClusterPushOutcome struct {
	ClusterID    uuid.UUID
	ClusterName  string
	OK           bool
	ErrorMessage string
}

// PushAccountToAllClustersDetailed pushes the account JWT to every cluster
// attached to the operator and returns one structured outcome per cluster.
// Same semantics as PushAccountToAllClusters (per-cluster failures do not
// short-circuit), but the caller gets a per-cluster verdict instead of just
// the failures.
//
// Used by RotateScopedSigningKey (P3) so the operator sees lagging clusters
// inline in the rotation response instead of having to cross-reference the
// drift dashboard.
//
// Confirmed-unhealthy clusters (LastHealthCheck != nil && !Healthy) are
// short-circuited with the last health-check error attached — mirrors the
// P9/P10 pattern. The fresh-cluster window (Healthy=false but never
// health-checked yet) is NOT short-circuited; those get an actual push
// attempt.
func (s *ClusterService) PushAccountToAllClustersDetailed(ctx context.Context, operatorID uuid.UUID, account *entities.Account) []ClusterPushOutcome {
	if account == nil || account.JWT == "" {
		return nil
	}
	clusters, err := s.repo.ListByOperator(ctx, operatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		// One synthetic outcome carrying the list error. Operator sees
		// "couldn't enumerate clusters" instead of an empty result that
		// would (wrongly) imply "no clusters attached".
		return []ClusterPushOutcome{{
			ErrorMessage: fmt.Sprintf("list clusters for operator %s: %v", operatorID, err),
		}}
	}
	outcomes := make([]ClusterPushOutcome, 0, len(clusters))
	for _, cluster := range clusters {
		if cluster.EncryptedCreds == "" {
			outcomes = append(outcomes, ClusterPushOutcome{
				ClusterID:    cluster.ID,
				ClusterName:  cluster.Name,
				OK:           false,
				ErrorMessage: "cluster has no system credentials configured; set creds first",
			})
			continue
		}
		// Confirmed-unhealthy short-circuit — same gate the P9 drift
		// scan uses. A cluster whose last health probe failed will
		// almost certainly fail this push for the same underlying
		// reason (resolver not configured, network unreachable, stale
		// creds). Skip with the last health-check error so the
		// operator immediately sees "this is a pre-existing cluster
		// problem, not a rotation problem."
		if cluster.LastHealthCheck != nil && !cluster.Healthy {
			msg := "cluster is unhealthy; resolve cluster connectivity first (`nisctl cluster get`)"
			if cluster.HealthCheckError != "" {
				msg = fmt.Sprintf("cluster is unhealthy: %s", cluster.HealthCheckError)
			}
			outcomes = append(outcomes, ClusterPushOutcome{
				ClusterID:    cluster.ID,
				ClusterName:  cluster.Name,
				OK:           false,
				ErrorMessage: msg,
			})
			continue
		}
		natsClient, _, openErr := s.openManagedCluster(ctx, cluster.ID)
		if openErr != nil {
			outcomes = append(outcomes, ClusterPushOutcome{
				ClusterID:    cluster.ID,
				ClusterName:  cluster.Name,
				OK:           false,
				ErrorMessage: fmt.Sprintf("open cluster: %v", openErr),
			})
			continue
		}
		pushErr := natsClient.PushAccountJWT(ctx, account)
		_ = natsClient.Close()
		if pushErr != nil {
			outcomes = append(outcomes, ClusterPushOutcome{
				ClusterID:    cluster.ID,
				ClusterName:  cluster.Name,
				OK:           false,
				ErrorMessage: friendlyPushError(pushErr),
			})
			continue
		}
		outcomes = append(outcomes, ClusterPushOutcome{
			ClusterID:   cluster.ID,
			ClusterName: cluster.Name,
			OK:          true,
		})
	}
	return outcomes
}

// friendlyPushError wraps the raw NATS push error with operator-actionable
// context for the common gotchas. The most confusing one is `nats: no
// responders available for request` against `$SYS.REQ.CLAIMS.UPDATE` —
// that's almost always "NATS is running but it's not configured as a JWT
// resolver" (e.g. open-mode dev container, missing `resolver: full` block
// in nats-server.conf). Without this hint the operator stares at a
// generic NATS protocol error.
func friendlyPushError(err error) string {
	raw := err.Error()
	if strings.Contains(raw, "no responders available") {
		return "push account JWT: " + raw + " — NATS responded but no resolver was listening on $SYS.REQ.CLAIMS.UPDATE; the cluster is likely running without a JWT resolver block in its config. Regenerate with `nisctl operator generate-include` and restart NATS with that config."
	}
	return "push account JWT: " + raw
}

// PushAccountToCluster pushes one account's JWT to one cluster. The
// per-cluster building block used by the A13-full job handlers; the existing
// fan-out methods (PushAccountToAllClusters, ...Detailed) cover the
// synchronous paths (rotate, manual sync, P9 reconcile) that need inline
// per-cluster outcomes.
//
// Returns nil on success, ErrNotFound (wrapped) if the cluster row is gone,
// a non-nil error otherwise. Skipping clusters with empty creds is a normal
// happy-path return: the caller (the handler) treats nil as "nothing to do
// here" and the substrate marks the job succeeded.
func (s *ClusterService) PushAccountToCluster(ctx context.Context, clusterID uuid.UUID, account *entities.Account) error {
	if account == nil || account.JWT == "" {
		return fmt.Errorf("nil or empty-JWT account")
	}
	natsClient, _, err := s.openManagedCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	defer func() { _ = natsClient.Close() }()
	if err := natsClient.PushAccountJWT(ctx, account); err != nil {
		return errors.New(friendlyPushError(err))
	}
	return nil
}

// DeleteAccountFromCluster sends an operator-signed delete-claim for one
// account public key to one cluster's resolver. Mirrors PushAccountToCluster
// but for the delete-claim path; used by the A13-full cluster.account.delete
// job handler.
func (s *ClusterService) DeleteAccountFromCluster(ctx context.Context, clusterID, operatorID uuid.UUID, accountPubkey string) error {
	if accountPubkey == "" {
		return fmt.Errorf("empty account public key")
	}
	operator, err := s.operatorRepo.GetByID(ctx, operatorID)
	if err != nil {
		return fmt.Errorf("get operator: %w", err)
	}
	deleteClaim, err := s.jwtService.GenerateDeleteClaimJWT(ctx, operator, []string{accountPubkey})
	if err != nil {
		return fmt.Errorf("generate delete claim: %w", err)
	}
	natsClient, _, err := s.openManagedCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	defer func() { _ = natsClient.Close() }()
	if err := natsClient.DeleteAccountJWT(ctx, deleteClaim); err != nil {
		return fmt.Errorf("delete on cluster: %w", err)
	}
	return nil
}

// openManagedCluster fetches a cluster, decrypts its system credentials, and opens a NATS
// connection. Returns the live client and the cluster entity. Caller MUST close the client.
func (s *ClusterService) openManagedCluster(ctx context.Context, id uuid.UUID) (*nats.Client, *entities.Cluster, error) {
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster.EncryptedCreds == "" {
		return nil, nil, fmt.Errorf("cluster has no system account credentials configured")
	}
	credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decrypt credentials: %w", err)
	}
	client, err := nats.NewClientFromCreds(cluster.ServerURLs, string(credsBytes), cluster.SkipVerifyTLS)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to NATS cluster: %w", err)
	}
	return client, cluster, nil
}

// SyncCluster pushes all account JWTs for the operator to the NATS cluster resolver
// If prune is true, it also removes accounts from the resolver that are not in the database
func (s *ClusterService) SyncCluster(ctx context.Context, id uuid.UUID, prune bool) (result *SyncResult, retErr error) {
	syncStart := time.Now() // duration measurement only; tz-irrelevant
	defer func() {
		outcome := "ok"
		if retErr != nil || (result != nil && len(result.Errors) > 0) {
			outcome = "err"
		}
		metrics.Default().RecordClusterSyncDuration(ctx, time.Since(syncStart).Seconds(), outcome)
	}()

	natsClient, cluster, err := s.openManagedCluster(ctx, id)
	if err != nil {
		metrics.Default().RecordClusterSyncError(ctx, "open_cluster")
		return nil, err
	}
	defer func() { _ = natsClient.Close() }()

	// Get all accounts for this operator
	accounts, err := s.accountRepo.ListByOperator(ctx, cluster.OperatorID, repositories.ListOptions{
		Limit:  1000, // TODO: Handle pagination if needed
		Offset: 0,
	})
	if err != nil {
		metrics.Default().RecordClusterSyncError(ctx, "list_accounts")
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	result = &SyncResult{
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

	if s.factory != nil {
		if err := events.EmitSystem(ctx, s.factory, events.Event{
			Type:         entities.EventTypeClusterSynced,
			OperatorID:   &cluster.OperatorID,
			ResourceType: "cluster",
			ResourceID:   cluster.ID.String(),
			Payload:      map[string]any{"name": cluster.Name, "synced_accounts": result.AccountsUpdated, "pruned": prune},
		}); err != nil {
			return nil, fmt.Errorf("emit cluster.synced: %w", err)
		}
	}

	return result, nil
}

// ListResolverAccounts lists all account public keys currently on the NATS resolver
func (s *ClusterService) ListResolverAccounts(ctx context.Context, clusterID uuid.UUID) ([]string, error) {
	natsClient, _, err := s.openManagedCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = natsClient.Close() }()

	return natsClient.ListAccountsFromResolver(ctx)
}

// DeleteResolverAccount removes an account from the NATS resolver
func (s *ClusterService) DeleteResolverAccount(ctx context.Context, clusterID uuid.UUID, publicKey string) error {
	natsClient, cluster, err := s.openManagedCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	defer func() { _ = natsClient.Close() }()

	// Safety check: don't allow deleting the system account
	if publicKey == cluster.SystemAccountPubKey {
		return fmt.Errorf("cannot delete system account from resolver")
	}

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

// CheckClusterHealth checks if a cluster is reachable and updates its health status
func (s *ClusterService) CheckClusterHealth(ctx context.Context, id uuid.UUID) error {
	cluster, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	prevHealthy := cluster.Healthy
	healthy := false
	var healthErr string
	now := clock.Now()

	// Try to connect to the cluster, then probe the JWT resolver. Both must succeed
	// for the cluster to be considered healthy — a reachable NATS without a
	// configured resolver cannot accept account-JWT pushes, which is the whole
	// reason we manage this cluster.
	if cluster.EncryptedCreds != "" {
		natsClient, _, connErr := s.openManagedCluster(ctx, id)
		if connErr != nil {
			healthErr = connErr.Error()
			metrics.Default().RecordClusterHealthCheckFailure(ctx)
		} else {
			if probeErr := natsClient.ProbeResolver(ctx); probeErr != nil {
				healthErr = probeErr.Error()
				metrics.Default().RecordClusterHealthCheckFailure(ctx)
			} else {
				healthy = true
			}
			_ = natsClient.Close()
		}
	} else {
		healthErr = "no credentials configured"
		metrics.Default().RecordClusterHealthCheckFailure(ctx)
	}

	// Update health status
	cluster.Healthy = healthy
	cluster.LastHealthCheck = &now
	cluster.HealthCheckError = healthErr
	cluster.UpdatedAt = clock.Now()

	if err := s.repo.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to update cluster health status: %w", err)
	}

	if s.factory != nil && healthy != prevHealthy {
		payload := map[string]any{"name": cluster.Name, "healthy": healthy, "error": healthErr}
		if err := events.EmitSystem(ctx, s.factory, events.Event{
			Type:         entities.EventTypeClusterHealthChanged,
			OperatorID:   &cluster.OperatorID,
			ResourceType: "cluster",
			ResourceID:   cluster.ID.String(),
			Payload:      payload,
		}); err != nil {
			return fmt.Errorf("emit cluster.health_changed: %w", err)
		}
	}

	return nil
}

// Per-cluster health checks are now driven by the jobs substrate (A15):
// JobTypeClusterHealthSweep enumerates clusters and enqueues one
// JobTypeClusterHealthCheck per row. See
// internal/application/services/job_handlers_cluster_health.go. The previous
// in-process CheckAllClustersHealth ticker was removed because the substrate
// already provides per-row leader election (partial unique index +
// FOR UPDATE SKIP LOCKED claim) — a separate process-level singleton was
// redundant for the multi-replica case and load-bearing only as the trigger.
