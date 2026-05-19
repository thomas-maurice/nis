package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	natsclient "github.com/thomas-maurice/nis/internal/infrastructure/nats"
)

// JetStreamProbeStatus mirrors the proto enum so the service layer can express
// per-cluster outcomes without importing generated proto code.
type JetStreamProbeStatus int

const (
	JetStreamProbeStatusUnspecified JetStreamProbeStatus = iota
	JetStreamProbeStatusOK
	JetStreamProbeStatusUnreachable
	JetStreamProbeStatusNoJetStream
	JetStreamProbeStatusAccountNotFound
	JetStreamProbeStatusError
	// JetStreamProbeStatusNotActivated: the account's JWT has JS enabled but
	// the cluster has not yet activated per-account JS state for it. NATS
	// initialises that state lazily on the first client connect or first JS
	// API call from the account. Differentiated from ACCOUNT_NOT_FOUND
	// (which is genuine drift) so the UI can show "0 / max" bars + a hint
	// to connect once.
	JetStreamProbeStatusNotActivated
)

// ClusterJetStreamUsage is the per-cluster outcome of a live JSZ probe.
// Identical shape to the proto message; the handler converts.
type ClusterJetStreamUsage struct {
	ClusterID    uuid.UUID
	ClusterName  string
	Status       JetStreamProbeStatus
	ErrorMessage string
	Usage        *natsclient.JetStreamAccountInfo
}

// GetAccountJetStreamUsageOptions controls probe behaviour. Zero-value is fine.
type GetAccountJetStreamUsageOptions struct {
	// IncludeUnhealthy forces a dial attempt against clusters whose last
	// health-check tick marked them unreachable. Default (false) short-circuits
	// those with UNREACHABLE without paying the dial timeout — refresh latency
	// matters here, the page is interactive.
	IncludeUnhealthy bool
}

// JS-usage probe budget. Per-cluster timeout matches the existing health-check
// pattern (3s); parent budget caps total wall time so the RPC can't outlive a
// reasonable user-refresh window even if every cluster goes slow simultaneously.
const (
	jsUsageProbeParentTimeout = 8 * time.Second
	jsUsageProbeClusterTimeout = 3 * time.Second
)

// GetAccountJetStreamUsage queries every cluster attached to the account's
// operator and returns per-cluster JetStream usage. Per-cluster failures are
// reported via Status, NOT propagated as the top-level error — that lets the
// UI show partial results when one cluster of many is down. The only top-level
// errors are repository failures (e.g. "account not found in NIS DB"). The
// caller (handler) is responsible for the RBAC gate via PermissionService.
//
// No DB writes; no events emitted (this is a read-only inspection).
func (s *AccountService) GetAccountJetStreamUsage(
	ctx context.Context,
	accountID uuid.UUID,
	opts GetAccountJetStreamUsageOptions,
) ([]*ClusterJetStreamUsage, error) {
	account, err := s.factory.AccountRepository().GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	clusters, err := s.factory.ClusterRepository().ListByOperator(ctx, account.OperatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters for operator: %w", err)
	}
	if len(clusters) == 0 {
		return []*ClusterJetStreamUsage{}, nil
	}

	parentCtx, cancel := context.WithTimeout(ctx, jsUsageProbeParentTimeout)
	defer cancel()

	results := make([]*ClusterJetStreamUsage, len(clusters))
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(parentCtx)
	for i, cluster := range clusters {
		i, cluster := i, cluster
		g.Go(func() error {
			result := s.probeClusterJetStream(gctx, cluster, account.PublicKey, opts.IncludeUnhealthy)
			// Promote "account not found" to "not activated" when NIS DB
			// says JS IS enabled for the account. NATS lazily initialises
			// per-account JS state; until a client connects with the
			// account's creds (or makes a JS API call), JSZ returns
			// "account not found" even though the JWT is on the resolver.
			// This is the single most surprising behaviour for operators
			// who just enabled JS on an account and refresh the UI.
			if result.Status == JetStreamProbeStatusAccountNotFound && account.JetStreamEnabled {
				result.Status = JetStreamProbeStatusNotActivated
				result.ErrorMessage = "JetStream is enabled for this account but has not been activated on this cluster yet — connect once with an account credential (or publish to a JS subject) to initialise state"
			}
			mu.Lock()
			results[i] = result
			mu.Unlock()
			return nil // never propagate; classification lives in Status
		})
	}
	// errgroup.Wait never returns an error here (probes swallow them into Status).
	_ = g.Wait()

	sort.SliceStable(results, func(a, b int) bool {
		return results[a].ClusterName < results[b].ClusterName
	})
	return results, nil
}

// probeClusterJetStream is the single-cluster worker. It always returns a
// non-nil result with a populated Status; never returns an error.
func (s *AccountService) probeClusterJetStream(
	ctx context.Context,
	cluster *entities.Cluster,
	accountPublicKey string,
	includeUnhealthy bool,
) *ClusterJetStreamUsage {
	out := &ClusterJetStreamUsage{
		ClusterID:   cluster.ID,
		ClusterName: cluster.Name,
	}

	// Short-circuit on clusters confirmed unhealthy by the 60s health-check
	// loop. "Confirmed" requires a non-nil LastHealthCheck — a cluster row
	// created less than 60s ago has Healthy=false purely because the loop
	// hasn't run yet, and treating that as unreachable would make every
	// freshly-created cluster permanently grey in the UI until the first
	// health tick. Caller can still force a dial via includeUnhealthy=true.
	if !includeUnhealthy && cluster.LastHealthCheck != nil && !cluster.Healthy {
		out.Status = JetStreamProbeStatusUnreachable
		out.ErrorMessage = "cluster marked unhealthy by last health check"
		return out
	}

	if cluster.EncryptedCreds == "" {
		out.Status = JetStreamProbeStatusError
		out.ErrorMessage = "cluster has no system account credentials configured"
		return out
	}
	credsBytes, err := s.encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		out.Status = JetStreamProbeStatusError
		out.ErrorMessage = fmt.Sprintf("decrypt creds: %v", err)
		return out
	}
	client, err := natsclient.NewClientFromCreds(cluster.ServerURLs, string(credsBytes), cluster.SkipVerifyTLS)
	if err != nil {
		out.Status = JetStreamProbeStatusUnreachable
		out.ErrorMessage = fmt.Sprintf("dial: %v", err)
		return out
	}
	defer func() {
		_ = client.Close()
	}()

	probeCtx, cancel := context.WithTimeout(ctx, jsUsageProbeClusterTimeout)
	defer cancel()

	info, err := client.QueryAccountJetStreamInfo(probeCtx, accountPublicKey)
	if err != nil {
		switch {
		case errors.Is(err, natsclient.ErrJetStreamUnreachable):
			out.Status = JetStreamProbeStatusUnreachable
		case errors.Is(err, natsclient.ErrJetStreamAccountNotFound):
			out.Status = JetStreamProbeStatusAccountNotFound
		case errors.Is(err, natsclient.ErrJetStreamNotEnabled):
			out.Status = JetStreamProbeStatusNoJetStream
		default:
			out.Status = JetStreamProbeStatusError
		}
		out.ErrorMessage = err.Error()
		logging.LogFromContext(ctx).Debug("jetstream usage probe failed",
			"cluster_id", cluster.ID,
			"cluster_name", cluster.Name,
			"account_public_key", accountPublicKey,
			"err", err,
		)
		return out
	}

	out.Status = JetStreamProbeStatusOK
	out.Usage = info
	return out
}
