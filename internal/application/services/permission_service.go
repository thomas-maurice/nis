package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// PermissionService is the fine-grained scope check that runs AFTER the Casbin
// middleware has approved a role/resource/action triple. Casbin enforces "this
// role can do this action on this resource type"; this service enforces "this
// particular api-user can touch THIS particular operator/account/user."
//
// The Can* methods below are intentionally thin — they all delegate to one of
// three primitives:
//
//	requireRole(apiUser, ...)              admin-only operations
//	ownsOperator(ctx, apiUser, opID)       does this user have authority over the operator
//	ownsAccount(ctx, apiUser, acctID)      does this user have authority over the account
//
// Adding a new permission method should be a 2–5 line composition of these.
// If you find yourself writing a 3-arm role switch in a new Can* method, stop
// and add a helper instead — every duplicated switch invites a subtle scope leak.
type PermissionService struct {
	operatorRepo repositories.OperatorRepository
	accountRepo  repositories.AccountRepository
	userRepo     repositories.UserRepository
}

// NewPermissionService creates a new PermissionService
func NewPermissionService(
	operatorRepo repositories.OperatorRepository,
	accountRepo repositories.AccountRepository,
	userRepo repositories.UserRepository,
) *PermissionService {
	return &PermissionService{
		operatorRepo: operatorRepo,
		accountRepo:  accountRepo,
		userRepo:     userRepo,
	}
}

// Permission errors
var (
	ErrPermissionDenied = fmt.Errorf("permission denied")
	ErrInvalidScope     = fmt.Errorf("invalid scope for user role")
)

// ---------------------------------------------------------------------------
// Helpers — keep these the only place that knows the role hierarchy.
// ---------------------------------------------------------------------------

// requireRole denies access unless apiUser holds at least one of the listed roles.
// Use this for admin-only operations and as a fast deny for unauthenticated paths.
func (s *PermissionService) requireRole(apiUser *entities.APIUser, allowed ...entities.APIUserRole) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	for _, r := range allowed {
		if apiUser.Role == r {
			return nil
		}
	}
	return fmt.Errorf("%w: requires role in %v, have %q", ErrPermissionDenied, allowed, apiUser.Role)
}

// ownsOperator answers "can this api-user act on this operator?"
//
//	admin           always yes (subject to requireRole on the caller side)
//	operator-admin  yes iff apiUser.OperatorID == operatorID
//	account-admin   yes iff apiUser.AccountID's account.OperatorID == operatorID
func (s *PermissionService) ownsOperator(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) (bool, error) {
	if apiUser == nil {
		return false, nil
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return true, nil
	case entities.RoleOperatorAdmin:
		return apiUser.OperatorID != nil && *apiUser.OperatorID == operatorID, nil
	case entities.RoleAccountAdmin:
		if apiUser.AccountID == nil {
			return false, nil
		}
		account, err := s.accountRepo.GetByID(ctx, *apiUser.AccountID)
		if err != nil {
			return false, fmt.Errorf("failed to get account: %w", err)
		}
		return account.OperatorID == operatorID, nil
	}
	return false, nil
}

// ownsAccount answers "can this api-user act on this account?"
//
//	admin           always yes
//	operator-admin  yes iff the account's operator is the user's operator
//	account-admin   yes iff apiUser.AccountID == accountID
func (s *PermissionService) ownsAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) (bool, error) {
	if apiUser == nil {
		return false, nil
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return true, nil
	case entities.RoleOperatorAdmin:
		account, err := s.accountRepo.GetByID(ctx, accountID)
		if err != nil {
			return false, fmt.Errorf("failed to get account: %w", err)
		}
		return apiUser.OperatorID != nil && *apiUser.OperatorID == account.OperatorID, nil
	case entities.RoleAccountAdmin:
		return apiUser.AccountID != nil && *apiUser.AccountID == accountID, nil
	}
	return false, nil
}

// denyf is a tiny convenience for building permission-denied errors with context.
func denyf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrPermissionDenied}, args...)...)
}

// ---------------------------------------------------------------------------
// Operators
// ---------------------------------------------------------------------------

// CanCreateOperator: admin only (operators are a system-level concept).
func (s *PermissionService) CanCreateOperator(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanReadOperator: every authenticated role can read the operator they belong to.
func (s *PermissionService) CanReadOperator(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	ok, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot read operator %s", operatorID)
	}
	return nil
}

// CanUpdateOperator: admin only.
func (s *PermissionService) CanUpdateOperator(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	_ = operatorID // reserved for future operator-admin self-update; admin-only today
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanDeleteOperator: admin only.
func (s *PermissionService) CanDeleteOperator(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	_ = operatorID // signature kept for symmetry with the other Can*Operator calls
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanListOperators: every authenticated user can ask; SQL-level scope in ListPage narrows the result.
func (s *PermissionService) CanListOperators(apiUser *entities.APIUser) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	return nil
}

// FilterOperators returns only the operators visible to apiUser.
// Retained for use by SearchService (P11). The list handlers use SQL-level
// scope via ListPage instead.
func (s *PermissionService) FilterOperators(ctx context.Context, apiUser *entities.APIUser, operators []*entities.Operator) ([]*entities.Operator, error) {
	out := make([]*entities.Operator, 0, len(operators))
	for _, op := range operators {
		ok, err := s.ownsOperator(ctx, apiUser, op.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, op)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

// CanCreateAccount: admin or the operator-admin scoped to operatorID.
func (s *PermissionService) CanCreateAccount(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOperatorAdmin:
		if apiUser.OperatorID == nil || *apiUser.OperatorID != operatorID {
			return denyf("operator admin can only create accounts in their own operator")
		}
		return nil
	}
	return denyf("only admin or operator-admin can create accounts")
}

// CanReadAccount: anyone who owns the account.
func (s *PermissionService) CanReadAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	ok, err := s.ownsAccount(ctx, apiUser, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot read account %s", accountID)
	}
	return nil
}

// CanUpdateAccount: admin or the operator-admin whose operator owns the account.
// (Account-admins cannot update their own account — that's a separate proposal.)
func (s *PermissionService) CanUpdateAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot update accounts")
	}
	ok, err := s.ownsAccount(ctx, apiUser, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot update account %s", accountID)
	}
	return nil
}

// CanDeleteAccount: admin only (data-loss guard).
func (s *PermissionService) CanDeleteAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	_ = ctx
	_ = accountID // signature kept for future cascade auditing
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// FilterAccounts returns only the accounts visible to apiUser.
// Retained for use by SearchService (P11). The list handlers use SQL-level
// scope via ListPage instead.
func (s *PermissionService) FilterAccounts(ctx context.Context, apiUser *entities.APIUser, accounts []*entities.Account) ([]*entities.Account, error) {
	out := make([]*entities.Account, 0, len(accounts))
	for _, acc := range accounts {
		ok, err := s.ownsAccount(ctx, apiUser, acc.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, acc)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// CanCreateUser: any role that owns the account.
func (s *PermissionService) CanCreateUser(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	ok, err := s.ownsAccount(ctx, apiUser, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot create user in account %s", accountID)
	}
	return nil
}

// CanReadUser: any role that owns the user's account.
func (s *PermissionService) CanReadUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	ok, err := s.ownsAccount(ctx, apiUser, user.AccountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot read user %s", userID)
	}
	return nil
}

// CanUpdateUser: any role that owns the user's account.
func (s *PermissionService) CanUpdateUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	ok, err := s.ownsAccount(ctx, apiUser, user.AccountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot update user %s", userID)
	}
	return nil
}

// CanDeleteUser: admin or operator-admin owning the user's account (account-admin cannot delete).
func (s *PermissionService) CanDeleteUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot delete users")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	ok, err := s.ownsAccount(ctx, apiUser, user.AccountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot delete user %s", userID)
	}
	return nil
}

// CanRevokeUser: admin or operator-admin owning the user's account.
// Account-admins cannot revoke (mirrors CanDeleteUser semantics — revocation
// is a security-impacting mutation that touches the parent account JWT).
func (s *PermissionService) CanRevokeUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot revoke users")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	ok, err := s.ownsAccount(ctx, apiUser, user.AccountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot revoke user %s", userID)
	}
	return nil
}

// CanRegenerateUserCredentials: any role that owns the user's account. Mirrors
// CanUpdateUser — regenerating credentials is the credential equivalent of an
// update, not a deletion. Account-admins CAN regenerate creds for their own
// users (otherwise tenants can't recover from a leaked .creds without going
// up the chain).
func (s *PermissionService) CanRegenerateUserCredentials(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	return s.CanUpdateUser(ctx, apiUser, userID)
}

// CanSetOperatorJWTPolicy: admin only. The JWT lifecycle policy is a global
// per-operator setting and changing it affects every user in the operator —
// keep the bar high until there's an explicit operator-admin self-policy
// story.
func (s *PermissionService) CanSetOperatorJWTPolicy(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	_ = operatorID // reserved for future operator-admin self-update
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanRunJWTExpirySweep: admin only. Useful for tests and for ops who want to
// force an immediate sweep without waiting for the periodic tick.
func (s *PermissionService) CanRunJWTExpirySweep(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// FilterUsers returns only the users visible to apiUser.
// Retained for use by SearchService (P11). The list handlers use SQL-level
// scope via ListPage instead. On a per-row account lookup failure, the
// user is dropped (best-effort — a single account gone should not abort
// the entire search result).
func (s *PermissionService) FilterUsers(ctx context.Context, apiUser *entities.APIUser, users []*entities.User) ([]*entities.User, error) {
	out := make([]*entities.User, 0, len(users))
	for _, u := range users {
		ok, err := s.ownsAccount(ctx, apiUser, u.AccountID)
		if err != nil {
			// Best-effort — skip on lookup failure rather than failing the whole list.
			continue
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Clusters
// ---------------------------------------------------------------------------

// CanCreateCluster / CanUpdateCluster / CanDeleteCluster: admin only (clusters
// hold encrypted system credentials, so their lifecycle is system-level).
func (s *PermissionService) CanCreateCluster(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

func (s *PermissionService) CanUpdateCluster(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

func (s *PermissionService) CanDeleteCluster(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanReadCluster: any role that owns the cluster's operator.
func (s *PermissionService) CanReadCluster(ctx context.Context, apiUser *entities.APIUser, cluster *entities.Cluster) error {
	if cluster == nil {
		return ErrPermissionDenied
	}
	ok, err := s.ownsOperator(ctx, apiUser, cluster.OperatorID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot read cluster %s", cluster.ID)
	}
	return nil
}

// CanSyncCluster: admin or operator-admin scoped to the cluster's operator.
// Account-admins can't trigger cluster-wide JWT pushes.
func (s *PermissionService) CanSyncCluster(ctx context.Context, apiUser *entities.APIUser, cluster *entities.Cluster) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot sync clusters")
	}
	ok, err := s.ownsOperator(ctx, apiUser, cluster.OperatorID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot sync cluster %s", cluster.ID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Other resources
// ---------------------------------------------------------------------------

// CanManageAPIUsers: admin only.
func (s *PermissionService) CanManageAPIUsers(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// ---------------------------------------------------------------------------
// Templates (P6)
// ---------------------------------------------------------------------------
//
// Templates are operator-scoped. Account-admin gets nothing (mirrors the
// "account-admin cannot manage scoped keys" precedent — if they can't
// see SSKs, exposing the templates that feed them buys nothing). Bump
// and detach actions on SSKs reuse CanManageScopedKeys; this section
// covers only template-side authority.

// CanManageTemplate: admin or operator-admin owning the operator (account-admin not allowed).
// Used for Create / Update / Delete / ApplyToScopedKey.
func (s *PermissionService) CanManageTemplate(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot manage templates")
	}
	ok, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot manage templates in operator %s", operatorID)
	}
	return nil
}

// CanReadTemplate: admin or operator-admin owning the operator. Same
// reasoning as CanManageTemplate — account-admin gets nothing.
func (s *PermissionService) CanReadTemplate(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot read templates")
	}
	ok, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot read templates in operator %s", operatorID)
	}
	return nil
}

// CanManageBackup: admin or operator-admin owning the operator
// (account-admin denied). Backups expose the entire operator tree
// including system account material; scoping below operator-admin
// would be incoherent.
func (s *PermissionService) CanManageBackup(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot manage backups")
	}
	ok, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot manage backups in operator %s", operatorID)
	}
	return nil
}

// CanReadBackup: same surface as CanManageBackup. Reading the metadata
// of a backup is read of the operator tree's surface area; we mirror
// CanManageBackup rather than splitting the permission.
func (s *PermissionService) CanReadBackup(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	return s.CanManageBackup(ctx, apiUser, operatorID)
}

// CanManageScopedKeys: admin or operator-admin owning the account (account-admin not allowed).
func (s *PermissionService) CanManageScopedKeys(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAccountAdmin {
		return denyf("account admins cannot manage scoped keys")
	}
	ok, err := s.ownsAccount(ctx, apiUser, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot manage scoped keys in account %s", accountID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// CanReadEvents: admin only in v1.
func (s *PermissionService) CanReadEvents(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// ---------------------------------------------------------------------------
// Webhook subscriptions
// ---------------------------------------------------------------------------

func (s *PermissionService) CanCreateWebhookSubscription(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if err := s.requireRole(apiUser, entities.RoleAdmin, entities.RoleOperatorAdmin); err != nil {
		return err
	}
	if apiUser.Role == entities.RoleAdmin {
		return nil
	}
	owns, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !owns {
		return denyf("operator-admin can only create webhook subscriptions in their own operator")
	}
	return nil
}

func (s *PermissionService) CanReadWebhookSubscription(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if err := s.requireRole(apiUser, entities.RoleAdmin, entities.RoleOperatorAdmin); err != nil {
		return err
	}
	if apiUser.Role == entities.RoleAdmin {
		return nil
	}
	owns, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !owns {
		return denyf("operator-admin can only read webhook subscriptions in their own operator")
	}
	return nil
}

func (s *PermissionService) CanUpdateWebhookSubscription(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if err := s.requireRole(apiUser, entities.RoleAdmin, entities.RoleOperatorAdmin); err != nil {
		return err
	}
	if apiUser.Role == entities.RoleAdmin {
		return nil
	}
	owns, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !owns {
		return denyf("operator-admin can only update webhook subscriptions in their own operator")
	}
	return nil
}

func (s *PermissionService) CanDeleteWebhookSubscription(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if err := s.requireRole(apiUser, entities.RoleAdmin, entities.RoleOperatorAdmin); err != nil {
		return err
	}
	if apiUser.Role == entities.RoleAdmin {
		return nil
	}
	owns, err := s.ownsOperator(ctx, apiUser, operatorID)
	if err != nil {
		return err
	}
	if !owns {
		return denyf("operator-admin can only delete webhook subscriptions in their own operator")
	}
	return nil
}

// ---------------------------------------------------------------------------
// API tokens (service-account tokens)
// ---------------------------------------------------------------------------

// roleRank assigns a numeric ceiling to each role so CanCreateAPIToken can refuse
// to mint a token with a role higher than the caller's. admin > operator-admin >
// account-admin. Unknown roles get 0 (cannot mint anything).
func roleRank(r entities.APIUserRole) int {
	switch r {
	case entities.RoleAdmin:
		return 3
	case entities.RoleOperatorAdmin:
		return 2
	case entities.RoleAccountAdmin:
		return 1
	}
	return 0
}

// CanCreateAPIToken enforces the privilege escalation guard: the caller may not
// mint a token whose role exceeds their own, and any operator/account scope on
// the token must lie within the caller's scope.
//
// admin           — may mint any role / any scope.
// operator-admin  — may mint operator-admin (own operator) or account-admin (account in own operator).
// account-admin   — may mint account-admin only, scoped to own account.
func (s *PermissionService) CanCreateAPIToken(ctx context.Context, apiUser *entities.APIUser, role entities.APIUserRole, operatorID, accountID *uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if !role.IsValid() {
		return fmt.Errorf("%w: invalid role %q", ErrPermissionDenied, role)
	}
	if roleRank(role) > roleRank(apiUser.Role) {
		return denyf("cannot mint token with role %q (caller is %q)", role, apiUser.Role)
	}
	switch role {
	case entities.RoleOperatorAdmin:
		if operatorID == nil {
			return fmt.Errorf("operator_id is required for operator-admin tokens")
		}
		owns, err := s.ownsOperator(ctx, apiUser, *operatorID)
		if err != nil {
			return err
		}
		if !owns {
			return denyf("cannot mint operator-admin token for operator %s", *operatorID)
		}
	case entities.RoleAccountAdmin:
		if accountID == nil {
			return fmt.Errorf("account_id is required for account-admin tokens")
		}
		owns, err := s.ownsAccount(ctx, apiUser, *accountID)
		if err != nil {
			return err
		}
		if !owns {
			return denyf("cannot mint account-admin token for account %s", *accountID)
		}
	}
	return nil
}

// CanReadAPIToken: admin reads any token; non-admins only their own.
func (s *PermissionService) CanReadAPIToken(apiUser *entities.APIUser, token *entities.APIToken) error {
	if apiUser == nil || token == nil {
		return ErrPermissionDenied
	}
	if apiUser.Role == entities.RoleAdmin {
		return nil
	}
	if token.CreatedByUserID == nil || *token.CreatedByUserID != apiUser.ID {
		return denyf("cannot read api token %s", token.ID)
	}
	return nil
}

// CanDeleteAPIToken (== revoke): admin revokes any; non-admins only their own.
func (s *PermissionService) CanDeleteAPIToken(apiUser *entities.APIUser, token *entities.APIToken) error {
	return s.CanReadAPIToken(apiUser, token)
}

// CanListAPITokens: any authenticated role can call list — the handler narrows
// the filter to the caller's own tokens for non-admins.
func (s *PermissionService) CanListAPITokens(apiUser *entities.APIUser) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	return nil
}
