package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// PermissionService is the fine-grained scope check that runs AFTER the authz
// middleware has approved a role/resource/action triple. The middleware's
// RolePolicy lookup (internal/application/authz/registry.go) enforces "this
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
	operatorRepo     repositories.OperatorRepository
	accountRepo      repositories.AccountRepository
	userRepo         repositories.UserRepository
	organizationRepo repositories.OrganizationRepository
}

// NewPermissionService creates a new PermissionService.
// organizationRepo is used to resolve org ownership for ownsOrganization and
// the org-admin cases in ownsOperator / ownsAccount.
func NewPermissionService(
	operatorRepo repositories.OperatorRepository,
	accountRepo repositories.AccountRepository,
	userRepo repositories.UserRepository,
	organizationRepo repositories.OrganizationRepository,
) *PermissionService {
	return &PermissionService{
		operatorRepo:     operatorRepo,
		accountRepo:      accountRepo,
		userRepo:         userRepo,
		organizationRepo: organizationRepo,
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

// ownsOrganization answers "can this api-user act on this organization?"
//
//	admin           always yes
//	org-admin       yes iff apiUser.OrganizationID == orgID
//	operator-admin  yes iff the operator's OrganizationID == orgID
//	account-admin   yes iff account's operator's OrganizationID == orgID
func (s *PermissionService) ownsOrganization(ctx context.Context, apiUser *entities.APIUser, orgID uuid.UUID) (bool, error) {
	if apiUser == nil {
		return false, nil
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return true, nil
	case entities.RoleOrgAdmin:
		return apiUser.OrganizationID != nil && *apiUser.OrganizationID == orgID, nil
	case entities.RoleOperatorAdmin:
		if apiUser.OperatorID == nil {
			return false, nil
		}
		op, err := s.operatorRepo.GetByID(ctx, *apiUser.OperatorID)
		if err != nil {
			return false, fmt.Errorf("failed to get operator: %w", err)
		}
		return op.OrganizationID == orgID, nil
	case entities.RoleAccountAdmin:
		if apiUser.AccountID == nil {
			return false, nil
		}
		account, err := s.accountRepo.GetByID(ctx, *apiUser.AccountID)
		if err != nil {
			return false, fmt.Errorf("failed to get account: %w", err)
		}
		op, err := s.operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return false, fmt.Errorf("failed to get operator for account: %w", err)
		}
		return op.OrganizationID == orgID, nil
	}
	return false, nil
}

// ownsOperator answers "can this api-user act on this operator?"
//
//	admin           always yes (subject to requireRole on the caller side)
//	org-admin       yes iff the operator's OrganizationID == apiUser.OrganizationID
//	operator-admin  yes iff apiUser.OperatorID == operatorID
//	account-admin   yes iff apiUser.AccountID's account.OperatorID == operatorID
func (s *PermissionService) ownsOperator(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) (bool, error) {
	if apiUser == nil {
		return false, nil
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return true, nil
	case entities.RoleOrgAdmin:
		if apiUser.OrganizationID == nil {
			return false, nil
		}
		op, err := s.operatorRepo.GetByID(ctx, operatorID)
		if err != nil {
			return false, fmt.Errorf("failed to get operator: %w", err)
		}
		return op.OrganizationID == *apiUser.OrganizationID, nil
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
//	org-admin       yes iff account's operator's OrganizationID == apiUser.OrganizationID
//	operator-admin  yes iff the account's operator is the user's operator
//	account-admin   yes iff apiUser.AccountID == accountID
func (s *PermissionService) ownsAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) (bool, error) {
	if apiUser == nil {
		return false, nil
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return true, nil
	case entities.RoleOrgAdmin:
		if apiUser.OrganizationID == nil {
			return false, nil
		}
		account, err := s.accountRepo.GetByID(ctx, accountID)
		if err != nil {
			return false, fmt.Errorf("failed to get account: %w", err)
		}
		op, err := s.operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return false, fmt.Errorf("failed to get operator for account: %w", err)
		}
		return op.OrganizationID == *apiUser.OrganizationID, nil
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

// CanCreateOperator: platform admin, or an org-admin (who may only create
// operators inside their own org). The handler pins the target org to the
// org-admin's OrganizationID, so a role check is sufficient here — the org
// binding is enforced where the org is resolved (resolveEffectiveOrg).
func (s *PermissionService) CanCreateOperator(apiUser *entities.APIUser) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		if apiUser.OrganizationID == nil {
			return denyf("org-admin has no organization binding")
		}
		return nil
	}
	return denyf("only admin or org-admin can create operators")
}

// CanImportOperator gates operator import (ExportService.ImportOperator and
// ImportFromNSC). Imported operators currently always land in the default
// organization (see the import path in ExportService; multi-org import routing
// is deferred), so a platform admin may always import, and an org-admin may
// import only when their own organization IS the default org — otherwise the
// imported operator would be created outside the org they administer.
func (s *PermissionService) CanImportOperator(apiUser *entities.APIUser) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		if apiUser.OrganizationID != nil && apiUser.OrganizationID.String() == entities.DefaultOrganizationID {
			return nil
		}
		return denyf("org-admin can only import into the default organization")
	}
	return denyf("only admin or org-admin can import operators")
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

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

// CanCreateAccount: admin, an org-admin whose org owns the operator, or the
// operator-admin scoped to operatorID.
func (s *PermissionService) CanCreateAccount(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		ok, err := s.ownsOperator(ctx, apiUser, operatorID)
		if err != nil {
			return err
		}
		if !ok {
			return denyf("org admin can only create accounts in operators within their own organization")
		}
		return nil
	case entities.RoleOperatorAdmin:
		if apiUser.OperatorID == nil || *apiUser.OperatorID != operatorID {
			return denyf("operator admin can only create accounts in their own operator")
		}
		return nil
	}
	return denyf("only admin, org-admin, or operator-admin can create accounts")
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

// roleRank returns the privilege rank of a role for ceiling checks. Delegates
// to entities.APIUserRole.Rank() — single source of truth.
// Total order: admin (4) > org-admin (3) > operator-admin (2) > account-admin (1) > unknown (0).
func roleRank(r entities.APIUserRole) int {
	return r.Rank()
}

// CanCreateAPIToken enforces the privilege escalation guard: the caller may not
// mint a token whose role exceeds their own, and any operator/account scope on
// the token must lie within the caller's scope.
//
// admin           — may mint any role / any scope.
// org-admin       — may mint org-admin or below, scoped within their own org.
//                   The minted token's scope must satisfy ownsOperator/ownsAccount
//                   when operatorID/accountID are set.
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
	case entities.RoleOrgAdmin:
		// org-admin tokens must be scoped to the caller's own org. No
		// operatorID/accountID narrowing is required — the org-level scope
		// is sufficient and carried by ScopeOrganizationID on the token.
		if apiUser.Role != entities.RoleAdmin {
			// Spec: org-admin callers may not mint tokens with rank >= their own.
			// The blanket check above handles rank >, so we additionally block
			// the equal-rank case (org-admin cannot mint org-admin tokens).
			if roleRank(role) >= roleRank(apiUser.Role) {
				return denyf("cannot mint token with role %q: org-admin may only mint tokens with strictly lower rank", role)
			}
			if apiUser.OrganizationID == nil {
				return denyf("cannot mint org-admin token: caller has no organization")
			}
		}
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

// ---------------------------------------------------------------------------
// Organizations
// ---------------------------------------------------------------------------

// CanCreateOrganization: admin only. Orgs are platform-level objects.
func (s *PermissionService) CanCreateOrganization(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanReadOrganization: admin or any role whose org == orgID.
func (s *PermissionService) CanReadOrganization(ctx context.Context, apiUser *entities.APIUser, orgID uuid.UUID) error {
	ok, err := s.ownsOrganization(ctx, apiUser, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return denyf("cannot read organization %s", orgID)
	}
	return nil
}

// CanUpdateOrganization: admin or org-admin that owns the org.
func (s *PermissionService) CanUpdateOrganization(ctx context.Context, apiUser *entities.APIUser, orgID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		ok, err := s.ownsOrganization(ctx, apiUser, orgID)
		if err != nil {
			return err
		}
		if !ok {
			return denyf("cannot update organization %s", orgID)
		}
		return nil
	}
	return denyf("only admin or org-admin can update organizations")
}

// CanDeleteOrganization: admin only (data-loss guard).
func (s *PermissionService) CanDeleteOrganization(apiUser *entities.APIUser) error {
	return s.requireRole(apiUser, entities.RoleAdmin)
}

// CanListOrganizations: any authenticated user — SQL scope narrows to own org
// for org-admin; lower roles see nothing unless admin.
func (s *PermissionService) CanListOrganizations(apiUser *entities.APIUser) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	return nil
}

// ---------------------------------------------------------------------------
// SSO configuration
// ---------------------------------------------------------------------------

// CanManageSSO: admin or org-admin that owns the org.
func (s *PermissionService) CanManageSSO(ctx context.Context, apiUser *entities.APIUser, orgID uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		ok, err := s.ownsOrganization(ctx, apiUser, orgID)
		if err != nil {
			return err
		}
		if !ok {
			return denyf("cannot manage SSO for organization %s", orgID)
		}
		return nil
	}
	return denyf("only admin or org-admin can manage SSO configuration")
}

// CanReadSSO: admin or org-admin that owns the org.
func (s *PermissionService) CanReadSSO(ctx context.Context, apiUser *entities.APIUser, orgID uuid.UUID) error {
	return s.CanManageSSO(ctx, apiUser, orgID)
}

// ---------------------------------------------------------------------------
// API user management (gate methods — wired into AuthService in chunk 5)
// ---------------------------------------------------------------------------

// CanCreateAPIUser gates creation of a new api_user row. The caller may not
// create a user whose role rank >= their own, and org-admin may only create
// users in their own organization.
//
//	admin       — may create any role in any org.
//	org-admin   — may create roles with rank < caller's rank (not admin/org-admin),
//	              in their own org only.
//	others      — denied.
func (s *PermissionService) CanCreateAPIUser(apiUser *entities.APIUser, targetRole entities.APIUserRole, targetOrgID *uuid.UUID) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	if !targetRole.IsValid() {
		return fmt.Errorf("%w: invalid role %q", ErrPermissionDenied, targetRole)
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		if roleRank(targetRole) >= roleRank(apiUser.Role) {
			return denyf("cannot create api_user with role %q (caller is org-admin)", targetRole)
		}
		// Target must be scoped to the caller's org.
		if apiUser.OrganizationID == nil {
			return denyf("org-admin caller has no organization")
		}
		if targetOrgID == nil || *targetOrgID != *apiUser.OrganizationID {
			return denyf("org-admin can only create api_users in their own organization")
		}
		return nil
	}
	return denyf("only admin or org-admin can create api_users")
}

// CanReadAPIUser gates reading an existing api_user.
//
//	admin     — may read any user.
//	org-admin — may read users in their own org whose role rank < their own.
//	others    — denied.
func (s *PermissionService) CanReadAPIUser(apiUser *entities.APIUser, target *entities.APIUser) error {
	if apiUser == nil || target == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		if roleRank(target.Role) >= roleRank(apiUser.Role) {
			return denyf("cannot read api_user with role %q (caller is org-admin)", target.Role)
		}
		if apiUser.OrganizationID == nil {
			return denyf("org-admin caller has no organization")
		}
		if target.OrganizationID == nil || *target.OrganizationID != *apiUser.OrganizationID {
			return denyf("cannot read api_user in a different organization")
		}
		return nil
	}
	return denyf("only admin or org-admin can read api_users")
}

// CanUpdateAPIUser gates updating an existing api_user (password or permissions).
//
//	admin     — may update any user.
//	org-admin — may update users in their own org whose role rank < their own.
//	others    — denied.
func (s *PermissionService) CanUpdateAPIUser(apiUser *entities.APIUser, target *entities.APIUser) error {
	if apiUser == nil || target == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		if roleRank(target.Role) >= roleRank(apiUser.Role) {
			return denyf("cannot update api_user with role %q (caller is org-admin)", target.Role)
		}
		if apiUser.OrganizationID == nil {
			return denyf("org-admin caller has no organization")
		}
		if target.OrganizationID == nil || *target.OrganizationID != *apiUser.OrganizationID {
			return denyf("cannot update api_user in a different organization")
		}
		return nil
	}
	return denyf("only admin or org-admin can update api_users")
}

// CanDeleteAPIUser gates deletion of an api_user.
//
//	admin     — may delete any user.
//	org-admin — may delete users in their own org whose role rank < their own.
//	others    — denied.
func (s *PermissionService) CanDeleteAPIUser(apiUser *entities.APIUser, target *entities.APIUser) error {
	if apiUser == nil || target == nil {
		return ErrPermissionDenied
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return nil
	case entities.RoleOrgAdmin:
		if roleRank(target.Role) >= roleRank(apiUser.Role) {
			return denyf("cannot delete api_user with role %q (caller is org-admin)", target.Role)
		}
		if apiUser.OrganizationID == nil {
			return denyf("org-admin caller has no organization")
		}
		if target.OrganizationID == nil || *target.OrganizationID != *apiUser.OrganizationID {
			return denyf("cannot delete api_user in a different organization")
		}
		return nil
	}
	return denyf("only admin or org-admin can delete api_users")
}
