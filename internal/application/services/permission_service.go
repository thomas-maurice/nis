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

// CanListOperators: every authenticated user can ask, FilterOperators narrows the result.
func (s *PermissionService) CanListOperators(apiUser *entities.APIUser) error {
	if apiUser == nil {
		return ErrPermissionDenied
	}
	return nil
}

// FilterOperators returns only the operators visible to apiUser.
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

// FilterUsers returns only the users visible to apiUser. Note that this still
// makes O(n) account lookups; see proposal A7 (filter+cursor pagination) for the
// proper SQL-level fix.
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
