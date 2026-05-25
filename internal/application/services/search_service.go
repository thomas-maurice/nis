package services

// Global identity-tree search (P11).
//
// Pipeline:
//   1. Validate the query (trim, length 2..128) and the limit (default 20, cap 100).
//   2. Build an authz.Scope from the caller via authz.ScopeFromAPIUser.
//   3. Hit each requested repo's Search method with the scope + (escaped,
//      lowered) query. The repo enforces the scope at SQL-level via the same
//      WHERE narrowing ListPage uses — search and list cannot drift apart.
//
// RBAC narrowing happens entirely at the SQL layer; there is no post-fetch
// filter step in this file. The pre-A20 helpers FilterOperators /
// FilterAccounts / FilterUsers / ownsAccount / ownsOperator post-fetch loops
// were retired here once every repo grew a Scope-aware Search variant.
//
// Deliberate exclusions from the search surface (do NOT add these without an
// explicit re-review):
//   - api_users / api_tokens: credentials surface; not safe to expose by free-text.
//   - webhook_subscriptions: per-operator secrets surface; access already gated
//     by P4/A6 surface.
//   - events: audit log is admin-only and has its own filtered view.
//   - operator_age_recipients (P15): per-operator pubkey list; not secret, but
//     has its own per-operator surface (nisctl operator backup list-recipients).
//     Adding to global search would force a JOIN to filter by author + change
//     the threat model around recipient enumeration. Keep narrow.
//
// Authz: the SearchService RPC is registered as (search, read, KindPerRow)
// in authz/registry.go. RolePolicy grants all three roles `search.read`; the
// fine-grained scope isolation lives in the SQL WHERE clause of each repo
// Search (handler passes the authed user; service builds authz.Scope).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
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

// SearchResults groups matches by kind. Each slice is already narrowed at the
// repo layer via the caller's authz.Scope. OperatorNames + AccountOperators
// are side-band lookup tables so the UI can label each row with the owning
// operator without an extra round-trip (Accounts/Clusters carry operator_id
// natively; Users and ScopedSigningKeys only carry account_id, so they chain
// via AccountOperators → OperatorNames). Entries exist only for operators
// referenced by at least one row in the result set.
type SearchResults struct {
	Operators         []*entities.Operator
	Accounts          []*entities.Account
	Users             []*entities.User
	ScopedSigningKeys []*entities.ScopedSigningKey
	Clusters          []*entities.Cluster
	OperatorNames     map[string]string
	AccountOperators  map[string]string
}

// ErrSearchQueryInvalid is returned for empty / too-short / too-long queries.
// Surfaced as connect.CodeInvalidArgument by the handler.
var ErrSearchQueryInvalid = errors.New("search query must be 2..128 characters after trim")

// SearchService runs the global search query across the five identity-tree
// kinds. RBAC narrowing is delegated to each repo's Scope-aware Search.
type SearchService struct {
	factory persistence.RepositoryFactory
}

// NewSearchService constructs a SearchService.
func NewSearchService(factory persistence.RepositoryFactory) *SearchService {
	return &SearchService{factory: factory}
}

// Search runs a single global query for `apiUser`. `kinds` selects which
// surfaces to hit; nil/empty means all five. `limit` applies per kind and is
// clamped to [1, 100]; 0 means use the default (20).
//
// RBAC narrowing happens at the repo layer via authz.Scope; this method just
// builds the scope and dispatches. A nil apiUser yields the zero Scope, which
// every repo treats as "no rows" — same safe failure mode as the rest of the
// list surface.
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

	scope := authz.ScopeFromAPIUser(apiUser)
	want := kindSet(kinds)
	out := &SearchResults{}

	if want[SearchKindOperator] {
		got, err := s.factory.OperatorRepository().Search(ctx, scope, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search operators: %w", err)
		}
		out.Operators = got
	}

	if want[SearchKindAccount] {
		got, err := s.factory.AccountRepository().Search(ctx, scope, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search accounts: %w", err)
		}
		out.Accounts = got
	}

	if want[SearchKindUser] {
		got, err := s.factory.UserRepository().Search(ctx, scope, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search users: %w", err)
		}
		out.Users = got
	}

	if want[SearchKindScopedSigningKey] {
		got, err := s.factory.ScopedSigningKeyRepository().Search(ctx, scope, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search scoped signing keys: %w", err)
		}
		out.ScopedSigningKeys = got
	}

	if want[SearchKindCluster] {
		got, err := s.factory.ClusterRepository().Search(ctx, scope, trimmed, limit)
		if err != nil {
			return nil, fmt.Errorf("search clusters: %w", err)
		}
		out.Clusters = got
	}

	if err := s.populateOperatorContext(ctx, out); err != nil {
		return nil, fmt.Errorf("populate operator context: %w", err)
	}

	return out, nil
}

// populateOperatorContext fills OperatorNames and AccountOperators so the UI
// can render the owning operator on every row. For Users and SSKs we need to
// chain account_id → operator_id first; Accounts and Clusters carry the
// operator_id natively. Lookups are deduped by ID. Per-row resolution errors
// are tolerated (the row just renders without an operator label) — search is
// an inspection surface, not a write path, and the rows themselves are
// already scoped at the repo layer.
func (s *SearchService) populateOperatorContext(ctx context.Context, r *SearchResults) error {
	accountIDs := make(map[uuid.UUID]struct{})
	operatorIDs := make(map[uuid.UUID]struct{})

	for _, a := range r.Accounts {
		operatorIDs[a.OperatorID] = struct{}{}
	}
	for _, c := range r.Clusters {
		operatorIDs[c.OperatorID] = struct{}{}
	}
	for _, o := range r.Operators {
		operatorIDs[o.ID] = struct{}{}
	}
	for _, u := range r.Users {
		accountIDs[u.AccountID] = struct{}{}
	}
	for _, k := range r.ScopedSigningKeys {
		accountIDs[k.AccountID] = struct{}{}
	}

	accountOperators := make(map[string]string, len(accountIDs))
	if len(accountIDs) > 0 {
		accRepo := s.factory.AccountRepository()
		for aid := range accountIDs {
			acc, err := accRepo.GetByID(ctx, aid)
			if err != nil {
				if errors.Is(err, repositories.ErrNotFound) {
					continue
				}
				return fmt.Errorf("lookup account %s: %w", aid, err)
			}
			accountOperators[aid.String()] = acc.OperatorID.String()
			operatorIDs[acc.OperatorID] = struct{}{}
		}
	}

	operatorNames := make(map[string]string, len(operatorIDs))
	if len(operatorIDs) > 0 {
		opRepo := s.factory.OperatorRepository()
		for oid := range operatorIDs {
			op, err := opRepo.GetByID(ctx, oid)
			if err != nil {
				if errors.Is(err, repositories.ErrNotFound) {
					continue
				}
				return fmt.Errorf("lookup operator %s: %w", oid, err)
			}
			operatorNames[oid.String()] = op.Name
		}
	}

	r.OperatorNames = operatorNames
	r.AccountOperators = accountOperators
	return nil
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
