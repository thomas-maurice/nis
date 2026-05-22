package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"golang.org/x/sync/errgroup"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	natsclient "github.com/thomas-maurice/nis/internal/infrastructure/nats"
)

// DriftStatus is the application-layer mirror of the proto enum so the service
// can classify (account, cluster) drift outcomes without importing generated
// proto code. The handler converts.
type DriftStatus int

const (
	DriftStatusUnspecified DriftStatus = iota
	DriftStatusInSync
	DriftStatusDBAhead
	DriftStatusOutOfBand
	DriftStatusMissingOnResolver
	DriftStatusUnreachable
	// DriftStatusOrphanOnResolver: the resolver has a JWT for an account
	// NIS has no row for. Surface for explicit cleanup — leaked .creds
	// minted under such a JWT will keep connecting indefinitely under the
	// default no-expiry policy. Rows of this kind have empty AccountID
	// and AccountName; only AccountPublicKey is populated.
	DriftStatusOrphanOnResolver
)

// AccountDriftRow is the per-account, per-cluster comparison result returned
// by ScanClusterDrift. Identical shape to the proto message.
type AccountDriftRow struct {
	AccountID        uuid.UUID
	AccountName      string
	AccountPublicKey string
	Status           DriftStatus
	// Unix seconds. Zero when the corresponding JWT could not be decoded
	// (NIS-side: should be impossible; resolver-side: covered by status).
	NISJwtIAT      int64
	ResolverJwtIAT int64
	NISJwtJTI      string
	ResolverJwtJTI string
	// Populated for non-OK statuses with the underlying error string.
	ErrorMessage string
}

// Drift-scan budget. Operators may have many accounts; one TLS+auth+ping
// connection per cluster, then bounded-parallel lookups multiplexed over it.
// Each lookup is cheap (one $SYS.REQ.ACCOUNT.<pk>.CLAIMS.LOOKUP request+reply),
// but capping concurrency keeps us polite to the resolver and avoids head-of-
// line blocking under a slow cluster.
const (
	clusterDriftParentTimeout      = 30 * time.Second
	clusterDriftPerAccountTimeout  = 1 * time.Second
	clusterDriftLookupConcurrency  = 10
	clusterDriftReconcileTimeout   = 5 * time.Second
)

// ScanClusterDrift compares each account-on-operator's NIS-DB JWT to the
// resolver-stored JWT on the given cluster. Returns one row per account,
// sorted by account name. IN_SYNC rows are filtered when includeInSync=false.
//
// No DB writes; no events. The only top-level errors are repository failures
// (e.g. cluster row missing). Per-cluster connection failures collapse into
// every row's Status (UNREACHABLE), not the top-level error — that lets a
// caller distinguish "we couldn't scan" from "scan succeeded, everything bad."
func (s *ClusterService) ScanClusterDrift(ctx context.Context, clusterID uuid.UUID, includeInSync bool) ([]*AccountDriftRow, error) {
	cluster, err := s.repo.GetByID(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster: %w", err)
	}

	accounts, err := s.accountRepo.ListByOperator(ctx, cluster.OperatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list accounts for operator: %w", err)
	}
	if len(accounts) == 0 {
		s.recordDriftScanOutcome(ctx, "ok")
		return []*AccountDriftRow{}, nil
	}

	parentCtx, cancel := context.WithTimeout(ctx, clusterDriftParentTimeout)
	defer cancel()

	// If the cluster row was marked unhealthy by the 60s health-check loop
	// (and we have evidence — LastHealthCheck != nil; otherwise it's just a
	// freshly-created row), short-circuit every account row as UNREACHABLE
	// rather than paying the dial timeout per account. Mirrors P10's pattern.
	if cluster.LastHealthCheck != nil && !cluster.Healthy {
		rows := make([]*AccountDriftRow, 0, len(accounts))
		for _, acc := range accounts {
			rows = append(rows, &AccountDriftRow{
				AccountID:        acc.ID,
				AccountName:      acc.Name,
				AccountPublicKey: acc.PublicKey,
				Status:           DriftStatusUnreachable,
				NISJwtIAT:        decodeJWTIat(acc.JWT),
				NISJwtJTI:        decodeJWTJTI(acc.JWT),
				ErrorMessage:     "cluster marked unhealthy by last health check",
			})
		}
		sortDriftRows(rows)
		s.recordDriftScanOutcome(ctx, "partial")
		s.recordDriftRowStatuses(ctx, rows)
		if !includeInSync {
			rows = filterInSync(rows)
		}
		return rows, nil
	}

	if cluster.EncryptedCreds == "" {
		rows := make([]*AccountDriftRow, 0, len(accounts))
		for _, acc := range accounts {
			rows = append(rows, &AccountDriftRow{
				AccountID:        acc.ID,
				AccountName:      acc.Name,
				AccountPublicKey: acc.PublicKey,
				Status:           DriftStatusUnreachable,
				NISJwtIAT:        decodeJWTIat(acc.JWT),
				NISJwtJTI:        decodeJWTJTI(acc.JWT),
				ErrorMessage:     "cluster has no system account credentials configured",
			})
		}
		sortDriftRows(rows)
		s.recordDriftScanOutcome(ctx, "partial")
		s.recordDriftRowStatuses(ctx, rows)
		if !includeInSync {
			rows = filterInSync(rows)
		}
		return rows, nil
	}

	credsBytes, err := s.encryptor.Decrypt(parentCtx, cluster.EncryptedCreds)
	if err != nil {
		return nil, fmt.Errorf("decrypt cluster credentials: %w", err)
	}
	client, err := natsclient.NewClientFromCreds(cluster.ServerURLs, string(credsBytes), cluster.SkipVerifyTLS)
	if err != nil {
		// Same shape as the short-circuit branch above: collapse to per-row
		// UNREACHABLE so the UI still shows the account list.
		rows := make([]*AccountDriftRow, 0, len(accounts))
		dialMsg := fmt.Sprintf("dial: %v", err)
		for _, acc := range accounts {
			rows = append(rows, &AccountDriftRow{
				AccountID:        acc.ID,
				AccountName:      acc.Name,
				AccountPublicKey: acc.PublicKey,
				Status:           DriftStatusUnreachable,
				NISJwtIAT:        decodeJWTIat(acc.JWT),
				NISJwtJTI:        decodeJWTJTI(acc.JWT),
				ErrorMessage:     dialMsg,
			})
		}
		sortDriftRows(rows)
		s.recordDriftScanOutcome(ctx, "partial")
		s.recordDriftRowStatuses(ctx, rows)
		if !includeInSync {
			rows = filterInSync(rows)
		}
		return rows, nil
	}
	defer func() { _ = client.Close() }()

	rows := make([]*AccountDriftRow, len(accounts))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(parentCtx)
	g.SetLimit(clusterDriftLookupConcurrency)

	for i, account := range accounts {
		i, account := i, account
		g.Go(func() error {
			row := s.probeOneAccountDrift(gctx, client, account)
			mu.Lock()
			rows[i] = row
			mu.Unlock()
			return nil // never propagate; classification lives in Status
		})
	}
	_ = g.Wait()

	// Orphan detection: ask the resolver for its full account list and
	// append one row per pubkey NIS has no record of. Failures here do
	// NOT poison the scan — orphan visibility is value-add on top of the
	// per-account comparison, but the per-account rows are the primary
	// signal. Best-effort log + continue.
	nisPubKeys := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		if a.PublicKey != "" {
			nisPubKeys[a.PublicKey] = true
		}
	}
	listCtx, listCancel := context.WithTimeout(parentCtx, clusterDriftPerAccountTimeout)
	resolverPKs, listErr := client.ListAccountsFromResolver(listCtx)
	listCancel()
	if listErr != nil {
		logging.LogFromContext(ctx).Debug("drift scan: resolver-side account listing failed (orphans not detected)",
			"cluster", cluster.Name,
			"err", listErr,
		)
	} else {
		for _, pk := range findOrphans(resolverPKs, nisPubKeys, cluster.SystemAccountPubKey) {
			rows = append(rows, &AccountDriftRow{
				AccountPublicKey: pk,
				Status:           DriftStatusOrphanOnResolver,
				ResolverJwtIAT:   0, // could decode if we LOOKUP'd each — not worth the latency for v1
			})
		}
	}

	sortDriftRows(rows)

	// Outcome metric: "ok" only when every row is IN_SYNC; "partial" if any
	// row carries a non-IN_SYNC status (drift, orphan, or per-account
	// unreachable).
	outcome := "ok"
	for _, r := range rows {
		if r.Status != DriftStatusInSync {
			outcome = "partial"
			break
		}
	}
	s.recordDriftScanOutcome(ctx, outcome)
	s.recordDriftRowStatuses(ctx, rows)

	if !includeInSync {
		rows = filterInSync(rows)
	}
	return rows, nil
}

func (s *ClusterService) probeOneAccountDrift(ctx context.Context, client *natsclient.Client, account *entities.Account) *AccountDriftRow {
	row := &AccountDriftRow{
		AccountID:        account.ID,
		AccountName:      account.Name,
		AccountPublicKey: account.PublicKey,
		NISJwtIAT:        decodeJWTIat(account.JWT),
		NISJwtJTI:        decodeJWTJTI(account.JWT),
	}

	if account.JWT == "" {
		// Should not happen in practice (every persisted account has a JWT),
		// but be defensive — without an NIS-side JWT we can't classify drift.
		row.Status = DriftStatusUnreachable
		row.ErrorMessage = "NIS database has no JWT for this account"
		return row
	}

	probeCtx, cancel := context.WithTimeout(ctx, clusterDriftPerAccountTimeout)
	defer cancel()

	resolverJWT, err := client.GetAccountJWT(probeCtx, account.PublicKey)
	if err != nil {
		if errors.Is(err, natsclient.ErrAccountNotOnResolver) {
			row.Status = DriftStatusMissingOnResolver
			row.ErrorMessage = err.Error()
			return row
		}
		row.Status = DriftStatusUnreachable
		row.ErrorMessage = fmt.Sprintf("lookup: %v", err)
		logging.LogFromContext(ctx).Debug("drift lookup failed",
			"account", account.Name,
			"public_key", account.PublicKey,
			"err", err,
		)
		return row
	}

	status, resIat, resJTI := compareJWTs(account.JWT, resolverJWT)
	row.Status = status
	row.ResolverJwtIAT = resIat
	row.ResolverJwtJTI = resJTI
	return row
}

// compareJWTs returns the drift classification plus the decoded iat/jti of the
// resolver JWT (used by the UI to surface "what NATS thinks" alongside the
// status). The NIS-side iat/jti are decoded by the caller from account.JWT.
//
// Fast path: byte equality on the encoded strings — this is the common
// IN_SYNC case after a successful sync, and decode allocation is wasted work.
// Slow path: decode both and delegate to classifyDecoded for the actual
// classification — that helper is what the unit tests target (the strings
// path is at the mercy of jwt v2's "stamp IssuedAt on every Encode" behaviour
// which makes hand-crafting test inputs awkward).
func compareJWTs(nisJWT, resolverJWT string) (DriftStatus, int64, string) {
	if nisJWT == resolverJWT {
		return DriftStatusInSync, decodeJWTIat(resolverJWT), decodeJWTJTI(resolverJWT)
	}

	nisClaims, nerr := jwt.DecodeAccountClaims(nisJWT)
	resClaims, rerr := jwt.DecodeAccountClaims(resolverJWT)
	if nerr != nil || rerr != nil {
		// We have both strings and they differ; one or both failed to
		// decode. Treat as out-of-band rather than unreachable — we did
		// reach the resolver and it gave us back something parseable as a
		// JWT in shape, just not as an account JWT we recognise.
		return DriftStatusOutOfBand, decodeJWTIat(resolverJWT), decodeJWTJTI(resolverJWT)
	}

	return classifyDecoded(nisClaims, resClaims)
}

// classifyDecoded is the post-decode branch of compareJWTs. Split out so unit
// tests can construct two *jwt.AccountClaims with deterministic iat/jti and
// exercise every classification branch without depending on Encode's
// IssuedAt-stamping behaviour.
func classifyDecoded(nisClaims, resClaims *jwt.AccountClaims) (DriftStatus, int64, string) {
	resIat := resClaims.IssuedAt
	resJTI := resClaims.ID

	if nisClaims.ID == resClaims.ID {
		// Content hashes match, raw strings don't. Means the encoded form
		// differs but the underlying claims are identical — e.g. resolver
		// re-encoded with different whitespace or header ordering. Treat
		// as in-sync; reconciliation would be a no-op.
		return DriftStatusInSync, resIat, resJTI
	}

	switch {
	case nisClaims.IssuedAt > resClaims.IssuedAt:
		return DriftStatusDBAhead, resIat, resJTI
	default:
		// Resolver's iat is >= NIS's, but content differs. Either someone
		// pushed to this resolver via a different tool, or NIS rebooted
		// from a stale backup and now lags. Both are operator concerns.
		return DriftStatusOutOfBand, resIat, resJTI
	}
}

// decodeJWTIat returns the issued-at unix seconds, or 0 on decode failure.
func decodeJWTIat(token string) int64 {
	if token == "" {
		return 0
	}
	claims, err := jwt.DecodeAccountClaims(token)
	if err != nil {
		return 0
	}
	return claims.IssuedAt
}

// decodeJWTJTI returns the JWT claim ID (jwt v2 content hash), or "" on failure.
func decodeJWTJTI(token string) string {
	if token == "" {
		return ""
	}
	claims, err := jwt.DecodeAccountClaims(token)
	if err != nil {
		return ""
	}
	return claims.ID
}

func sortDriftRows(rows []*AccountDriftRow) {
	// Named rows first (sorted by account name), then orphan rows (which
	// have an empty AccountName) sorted by public key. Mixing them with a
	// naive name compare clumps all the orphans at the top with empty
	// "Account" labels, which reads as a UI bug.
	sort.SliceStable(rows, func(a, b int) bool {
		ra, rb := rows[a], rows[b]
		if ra.AccountName == "" && rb.AccountName == "" {
			return ra.AccountPublicKey < rb.AccountPublicKey
		}
		if ra.AccountName == "" {
			return false
		}
		if rb.AccountName == "" {
			return true
		}
		return ra.AccountName < rb.AccountName
	})
}

func filterInSync(rows []*AccountDriftRow) []*AccountDriftRow {
	out := make([]*AccountDriftRow, 0, len(rows))
	for _, r := range rows {
		if r.Status == DriftStatusInSync {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (s *ClusterService) recordDriftScanOutcome(ctx context.Context, outcome string) {
	metrics.Default().RecordClusterDriftScan(ctx, outcome)
}

func (s *ClusterService) recordDriftRowStatuses(ctx context.Context, rows []*AccountDriftRow) {
	for _, r := range rows {
		metrics.Default().RecordClusterDriftResult(ctx, driftStatusLabel(r.Status))
	}
}

func driftStatusLabel(s DriftStatus) string {
	switch s {
	case DriftStatusInSync:
		return "in_sync"
	case DriftStatusDBAhead:
		return "db_ahead"
	case DriftStatusOutOfBand:
		return "out_of_band"
	case DriftStatusMissingOnResolver:
		return "missing_on_resolver"
	case DriftStatusUnreachable:
		return "unreachable"
	case DriftStatusOrphanOnResolver:
		return "orphan_on_resolver"
	}
	return "unspecified"
}

// findOrphans returns the resolver-side public keys that NIS has no
// corresponding account row for. Pure function — split out from the live
// resolver call so unit tests can pin the set-diff logic without standing
// up NATS.
//
// `systemAccountPubKey` is excluded because the cluster's $SYS account is
// auto-managed (created by `nisctl operator generate-include`) and lives on
// the resolver from day one regardless of NIS state — reporting it as an
// orphan would be a constant false positive.
func findOrphans(resolverPubKeys []string, nisAccountPubKeys map[string]bool, systemAccountPubKey string) []string {
	out := make([]string, 0)
	for _, pk := range resolverPubKeys {
		if pk == "" {
			continue
		}
		if pk == systemAccountPubKey {
			continue
		}
		if nisAccountPubKeys[pk] {
			continue
		}
		out = append(out, pk)
	}
	// Stable order so the UI doesn't shuffle orphan rows between refreshes.
	sort.Strings(out)
	return out
}

// ReconcileAccountOnCluster pushes one account's NIS-stored JWT to one specific
// cluster. The caller (handler) is responsible for the role gate; this method
// additionally enforces that the account belongs to the cluster's operator —
// without that guard, an admin could push account A's JWT to operator B's
// cluster, corrupting the foreign resolver.
//
// Emits cluster.account.synced on success. Errors propagate; no event on
// failure (the operator-visible failure surface is the RPC error).
func (s *ClusterService) ReconcileAccountOnCluster(ctx context.Context, clusterID, accountID uuid.UUID) error {
	cluster, err := s.repo.GetByID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}

	// Critical scope assertion: SyncCluster is safe because it iterates
	// accounts-by-operator (so a cross-operator push is structurally
	// impossible). Per-account reconcile has to add the check explicitly.
	if account.OperatorID != cluster.OperatorID {
		return fmt.Errorf("account %s does not belong to cluster %s's operator", account.ID, cluster.ID)
	}

	if account.JWT == "" {
		return fmt.Errorf("account %s has no JWT to push", account.Name)
	}
	if cluster.EncryptedCreds == "" {
		return fmt.Errorf("cluster %s has no system account credentials configured", cluster.Name)
	}

	natsClient, _, err := s.openManagedCluster(ctx, cluster.ID)
	if err != nil {
		return err
	}
	defer func() { _ = natsClient.Close() }()

	pushCtx, cancel := context.WithTimeout(ctx, clusterDriftReconcileTimeout)
	defer cancel()
	if err := natsClient.PushAccountJWT(pushCtx, account); err != nil {
		return fmt.Errorf("push account JWT: %w", err)
	}

	if s.factory != nil {
		// A13-full: trigger:"manual" mirrors the trigger:"auto" emitted
		// by the substrate-driven push path so webhook subscribers can
		// distinguish the two without subscribing to different event
		// types.
		if err := events.EmitSystem(ctx, s.factory, events.Event{
			Type:         entities.EventTypeClusterAccountSynced,
			OperatorID:   &cluster.OperatorID,
			AccountID:    &account.ID,
			ResourceType: "cluster",
			ResourceID:   cluster.ID.String(),
			Payload: map[string]any{
				"cluster_id":         cluster.ID.String(),
				"cluster_name":       cluster.Name,
				"account_id":         account.ID.String(),
				"account_name":       account.Name,
				"account_public_key": account.PublicKey,
				"trigger":            "manual",
			},
		}); err != nil {
			return fmt.Errorf("emit cluster.account.synced: %w", err)
		}
	}
	return nil
}
