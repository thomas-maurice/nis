package services

// Global identity-tree search (P11).
//
// Pipeline:
//   1. Validate the query (trim, length 2..128) and the limit (default 20, cap 100).
//   2. Hit each requested repo's Search method with the (escaped, lowered) query.
//   3. Narrow every result list by the caller's RBAC scope BEFORE returning so
//      operator-admin A never sees operator B's tree even when the LIKE query
//      would otherwise match. The narrowing is the existing PermissionService
//      helpers — same security boundary as every List RPC.
//
// Deliberate exclusions from the search surface (do NOT add these without an
// explicit re-review):
//   - api_users / api_tokens: credentials surface; not safe to expose by free-text.
//   - webhook_subscriptions: per-operator secrets surface; access already gated
//     by P4/A6 surface.
//   - events: audit log is admin-only and has its own filtered view.
//
// Casbin: the SearchService RPC routes through resource="search", action="read"
// (extractResourceAndAction). The policy file allows all three roles; fine-
// grained scope isolation lives entirely in this file (Filter* helpers below).
// Without this file's narrowing, a coarse Casbin allow would silently leak
// cross-operator data — that's the bug this service is responsible for not
// shipping.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

const (
	searchMinQueryLen     = 2
	searchMaxQueryLen     = 128
	searchDefaultLimit    = 20
	searchMaxLimitPerKind = 100
)

// SearchKind selects one entity surface to search.
type SearchKind int

const (
	SearchKindOperator SearchKind = iota
	SearchKindAccount
	SearchKindUser
	SearchKindScopedSigningKey
	SearchKindCluster
)

// SearchResults groups matches by kind. Each slice is already narrowed by the
// caller's RBAC scope.
type SearchResults struct {
	Operators         []*entities.Operator
	Accounts          []*entities.Account
	Users             []*entities.User
	ScopedSigningKeys []*entities.ScopedSigningKey
	Clusters          []*entities.Cluster
}

// ErrSearchQueryInvalid is returned for empty / too-short / too-long queries.
// Surfaced as connect.CodeInvalidArgument by the handler.
var ErrSearchQueryInvalid = errors.New("search query must be 2..128 characters after trim")

// SearchService runs the global search query across the five identity-tree
// kinds and applies RBAC narrowing before returning.
type SearchService struct {
	factory     persistence.RepositoryFactory
	permService *PermissionService
}

// NewSearchService constructs a SearchService. permService MUST be non-nil —
// the entire point of this service is to enforce cross-operator isolation.
func NewSearchService(factory persistence.RepositoryFactory, permService *PermissionService) *SearchService {
	return &SearchService{factory: factory, permService: permService}
}

// Search runs a single global query for `apiUser`. `kinds` selects which
// surfaces to hit; nil/empty means all five. `limit` applies per kind and is
// clamped to [1, 100]; 0 means use the default (20).
//
// RBAC narrowing happens here, not in the handler — every code path returns
// only entities the caller is allowed to read, regardless of what the LIKE
// query matched in the raw repo result.
func (s *SearchService) Search(ctx context.Context, apiUser *entities.APIUser, query string, kinds []SearchKind, limit int) (*SearchResults, error) {
	if apiUser == nil {
		return nil, ErrPermissionDenied
	}

	trimmed := strings.TrimSpace(query)
	if len(trimmed) < searchMinQueryLen || len(trimmed) > searchMaxQueryLen {
		return nil, ErrSearchQueryInvalid
	}

	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimitPerKind {
		limit = searchMaxLimitPerKind
	}

	want := kindSet(kinds)
	out := &SearchResults{}

	if want[SearchKindOperator] {
		raw, err := s.factory.OperatorRepository().Search(ctx, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search operators: %w", err)
		}
		filtered, err := s.permService.FilterOperators(ctx, apiUser, raw)
		if err != nil {
			return nil, fmt.Errorf("filter operators: %w", err)
		}
		out.Operators = filtered
	}

	if want[SearchKindAccount] {
		raw, err := s.factory.AccountRepository().Search(ctx, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search accounts: %w", err)
		}
		filtered, err := s.permService.FilterAccounts(ctx, apiUser, raw)
		if err != nil {
			return nil, fmt.Errorf("filter accounts: %w", err)
		}
		out.Accounts = filtered
	}

	if want[SearchKindUser] {
		raw, err := s.factory.UserRepository().Search(ctx, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search users: %w", err)
		}
		filtered, err := s.permService.FilterUsers(ctx, apiUser, raw)
		if err != nil {
			return nil, fmt.Errorf("filter users: %w", err)
		}
		out.Users = filtered
	}

	if want[SearchKindScopedSigningKey] {
		raw, err := s.factory.ScopedSigningKeyRepository().Search(ctx, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search scoped signing keys: %w", err)
		}
		out.ScopedSigningKeys = s.filterScopedSigningKeys(ctx, apiUser, raw)
	}

	if want[SearchKindCluster] {
		raw, err := s.factory.ClusterRepository().Search(ctx, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search clusters: %w", err)
		}
		out.Clusters = s.filterClusters(ctx, apiUser, raw)
	}

	return out, nil
}

// filterScopedSigningKeys narrows by ownsAccount — operator-admin sees keys
// in their operator's accounts; account-admin sees keys in their account.
// On a per-row lookup error we drop the row (consistent with FilterUsers).
func (s *SearchService) filterScopedSigningKeys(ctx context.Context, apiUser *entities.APIUser, keys []*entities.ScopedSigningKey) []*entities.ScopedSigningKey {
	out := make([]*entities.ScopedSigningKey, 0, len(keys))
	for _, k := range keys {
		ok, err := s.permService.ownsAccount(ctx, apiUser, k.AccountID)
		if err != nil || !ok {
			continue
		}
		out = append(out, k)
	}
	return out
}

// filterClusters narrows by ownsOperator on each cluster's operator_id.
func (s *SearchService) filterClusters(ctx context.Context, apiUser *entities.APIUser, clusters []*entities.Cluster) []*entities.Cluster {
	out := make([]*entities.Cluster, 0, len(clusters))
	for _, c := range clusters {
		ok, err := s.permService.ownsOperator(ctx, apiUser, c.OperatorID)
		if err != nil || !ok {
			continue
		}
		out = append(out, c)
	}
	return out
}

// kindSet expands the requested-kinds slice into a lookup map; an empty slice
// (or one containing only an UNSPECIFIED sentinel sourced from the proto) means
// "all kinds".
func kindSet(kinds []SearchKind) map[SearchKind]bool {
	if len(kinds) == 0 {
		return map[SearchKind]bool{
			SearchKindOperator:         true,
			SearchKindAccount:          true,
			SearchKindUser:             true,
			SearchKindScopedSigningKey: true,
			SearchKindCluster:          true,
		}
	}
	out := make(map[SearchKind]bool, len(kinds))
	for _, k := range kinds {
		out[k] = true
	}
	return out
}

// Compile-time assertion that the service relies on the existing repo factory
// surface — nothing in this file should ever reach into internal/* directly.
var _ = persistence.RepositoryFactory(nil)
var _ = repositories.ErrNotFound
