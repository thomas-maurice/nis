package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// PermissionService handles authorization checks with scope validation
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

// CanCreateOperator checks if user can create operators (admin only)
func (s *PermissionService) CanCreateOperator(apiUser *entities.APIUser) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can create operators", ErrPermissionDenied)
	}
	return nil
}

// CanReadOperator checks if user can read an operator
func (s *PermissionService) CanReadOperator(ctx context.Context, apiUser *entities.APIUser, operatorID uuid.UUID) error {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can read all operators
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only read their own operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != operatorID {
			return fmt.Errorf("%w: operator admin can only read their own operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		// Account admin can read their operator
		if apiUser.AccountID == nil {
			return ErrInvalidScope
		}
		account, err := s.accountRepo.GetByID(ctx, *apiUser.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}
		if account.OperatorID != operatorID {
			return fmt.Errorf("%w: account admin can only read their operator", ErrPermissionDenied)
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

// CanUpdateOperator checks if user can update an operator (admin only)
func (s *PermissionService) CanUpdateOperator(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can update operators", ErrPermissionDenied)
	}
	return nil
}

// CanDeleteOperator checks if user can delete an operator (admin only)
func (s *PermissionService) CanDeleteOperator(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can delete operators", ErrPermissionDenied)
	}
	return nil
}

// CanListOperators checks if user can list operators
func (s *PermissionService) CanListOperators(apiUser *entities.APIUser) error {
	// All authenticated users can list operators (but results will be filtered)
	return nil
}

// FilterOperators filters operators based on user permissions
func (s *PermissionService) FilterOperators(ctx context.Context, apiUser *entities.APIUser, operators []*entities.Operator) ([]*entities.Operator, error) {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can see all operators
		return operators, nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only see their own operator
		if apiUser.OperatorID == nil {
			return []*entities.Operator{}, nil
		}
		filtered := make([]*entities.Operator, 0)
		for _, op := range operators {
			if op.ID == *apiUser.OperatorID {
				filtered = append(filtered, op)
			}
		}
		return filtered, nil
	case entities.RoleAccountAdmin:
		// Account admin can only see their operator
		if apiUser.AccountID == nil {
			return []*entities.Operator{}, nil
		}
		account, err := s.accountRepo.GetByID(ctx, *apiUser.AccountID)
		if err != nil {
			return nil, fmt.Errorf("failed to get account: %w", err)
		}
		filtered := make([]*entities.Operator, 0)
		for _, op := range operators {
			if op.ID == account.OperatorID {
				filtered = append(filtered, op)
			}
		}
		return filtered, nil
	default:
		return []*entities.Operator{}, nil
	}
}

// CanCreateAccount checks if user can create an account in an operator
func (s *PermissionService) CanCreateAccount(apiUser *entities.APIUser, operatorID uuid.UUID) error {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can create accounts in any operator
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only create accounts in their own operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != operatorID {
			return fmt.Errorf("%w: operator admin can only create accounts in their own operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		return fmt.Errorf("%w: account admins cannot create accounts", ErrPermissionDenied)
	default:
		return ErrPermissionDenied
	}
}

// CanReadAccount checks if user can read an account
func (s *PermissionService) CanReadAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can read all accounts
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only read accounts in their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only read accounts in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		// Account admin can only read their own account
		if apiUser.AccountID == nil || *apiUser.AccountID != accountID {
			return fmt.Errorf("%w: account admin can only read their own account", ErrPermissionDenied)
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

// CanUpdateAccount checks if user can update an account
func (s *PermissionService) CanUpdateAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can update all accounts
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only update accounts in their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only update accounts in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		return fmt.Errorf("%w: account admins cannot update accounts", ErrPermissionDenied)
	default:
		return ErrPermissionDenied
	}
}

// CanDeleteAccount checks if user can delete an account
func (s *PermissionService) CanDeleteAccount(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	// Only admins can delete accounts (to prevent data loss)
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can delete accounts", ErrPermissionDenied)
	}
	return nil
}

// FilterAccounts filters accounts based on user permissions
func (s *PermissionService) FilterAccounts(ctx context.Context, apiUser *entities.APIUser, accounts []*entities.Account) ([]*entities.Account, error) {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can see all accounts
		return accounts, nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only see accounts in their operator
		if apiUser.OperatorID == nil {
			return []*entities.Account{}, nil
		}
		filtered := make([]*entities.Account, 0)
		for _, acc := range accounts {
			if acc.OperatorID == *apiUser.OperatorID {
				filtered = append(filtered, acc)
			}
		}
		return filtered, nil
	case entities.RoleAccountAdmin:
		// Account admin can only see their own account
		if apiUser.AccountID == nil {
			return []*entities.Account{}, nil
		}
		filtered := make([]*entities.Account, 0)
		for _, acc := range accounts {
			if acc.ID == *apiUser.AccountID {
				filtered = append(filtered, acc)
			}
		}
		return filtered, nil
	default:
		return []*entities.Account{}, nil
	}
}

// CanCreateUser checks if user can create a user in an account
func (s *PermissionService) CanCreateUser(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can create users in any account
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only create users in accounts of their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only create users in accounts of their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		// Account admin can only create users in their own account
		if apiUser.AccountID == nil || *apiUser.AccountID != accountID {
			return fmt.Errorf("%w: account admin can only create users in their own account", ErrPermissionDenied)
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

// CanReadUser checks if user can read a user
func (s *PermissionService) CanReadUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	account, err := s.accountRepo.GetByID(ctx, user.AccountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can read all users
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only read users in accounts of their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only read users in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		// Account admin can only read users in their own account
		if apiUser.AccountID == nil || *apiUser.AccountID != user.AccountID {
			return fmt.Errorf("%w: account admin can only read users in their account", ErrPermissionDenied)
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

// CanUpdateUser checks if user can update a user
func (s *PermissionService) CanUpdateUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	account, err := s.accountRepo.GetByID(ctx, user.AccountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can update all users
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only update users in accounts of their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only update users in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		// Account admin can only update users in their own account
		if apiUser.AccountID == nil || *apiUser.AccountID != user.AccountID {
			return fmt.Errorf("%w: account admin can only update users in their account", ErrPermissionDenied)
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

// CanDeleteUser checks if user can delete a user
func (s *PermissionService) CanDeleteUser(ctx context.Context, apiUser *entities.APIUser, userID uuid.UUID) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	account, err := s.accountRepo.GetByID(ctx, user.AccountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can delete all users
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only delete users in accounts of their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only delete users in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		return fmt.Errorf("%w: account admins cannot delete users", ErrPermissionDenied)
	default:
		return ErrPermissionDenied
	}
}

// FilterUsers filters users based on user permissions
func (s *PermissionService) FilterUsers(ctx context.Context, apiUser *entities.APIUser, users []*entities.User) ([]*entities.User, error) {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can see all users
		return users, nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only see users in accounts of their operator
		if apiUser.OperatorID == nil {
			return []*entities.User{}, nil
		}
		filtered := make([]*entities.User, 0)
		for _, user := range users {
			account, err := s.accountRepo.GetByID(ctx, user.AccountID)
			if err != nil {
				continue
			}
			if account.OperatorID == *apiUser.OperatorID {
				filtered = append(filtered, user)
			}
		}
		return filtered, nil
	case entities.RoleAccountAdmin:
		// Account admin can only see users in their own account
		if apiUser.AccountID == nil {
			return []*entities.User{}, nil
		}
		filtered := make([]*entities.User, 0)
		for _, user := range users {
			if user.AccountID == *apiUser.AccountID {
				filtered = append(filtered, user)
			}
		}
		return filtered, nil
	default:
		return []*entities.User{}, nil
	}
}

// CanCreateCluster checks if user can create a cluster (admin only)
func (s *PermissionService) CanCreateCluster(apiUser *entities.APIUser) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can create clusters", ErrPermissionDenied)
	}
	return nil
}

// CanReadCluster checks if user can read a cluster
func (s *PermissionService) CanReadCluster(ctx context.Context, apiUser *entities.APIUser, cluster *entities.Cluster) error {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can read all clusters
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only read clusters in their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != cluster.OperatorID {
			return fmt.Errorf("%w: operator admin can only read clusters in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		// Account admin can read clusters in their operator
		if apiUser.AccountID == nil {
			return ErrInvalidScope
		}
		account, err := s.accountRepo.GetByID(ctx, *apiUser.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}
		if account.OperatorID != cluster.OperatorID {
			return fmt.Errorf("%w: account admin can only read clusters in their operator", ErrPermissionDenied)
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

// CanUpdateCluster checks if user can update a cluster (admin only)
func (s *PermissionService) CanUpdateCluster(apiUser *entities.APIUser) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can update clusters", ErrPermissionDenied)
	}
	return nil
}

// CanDeleteCluster checks if user can delete a cluster (admin only)
func (s *PermissionService) CanDeleteCluster(apiUser *entities.APIUser) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can delete clusters", ErrPermissionDenied)
	}
	return nil
}

// CanSyncCluster checks if user can sync a cluster
func (s *PermissionService) CanSyncCluster(ctx context.Context, apiUser *entities.APIUser, cluster *entities.Cluster) error {
	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can sync all clusters
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only sync clusters in their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != cluster.OperatorID {
			return fmt.Errorf("%w: operator admin can only sync clusters in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		return fmt.Errorf("%w: account admins cannot sync clusters", ErrPermissionDenied)
	default:
		return ErrPermissionDenied
	}
}

// CanManageAPIUsers checks if user can manage API users (admin only)
func (s *PermissionService) CanManageAPIUsers(apiUser *entities.APIUser) error {
	if apiUser.Role != entities.RoleAdmin {
		return fmt.Errorf("%w: only admins can manage API users", ErrPermissionDenied)
	}
	return nil
}

// CanManageScopedKeys checks if user can manage scoped signing keys
func (s *PermissionService) CanManageScopedKeys(ctx context.Context, apiUser *entities.APIUser, accountID uuid.UUID) error {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	switch apiUser.Role {
	case entities.RoleAdmin:
		// Admin can manage all scoped keys
		return nil
	case entities.RoleOperatorAdmin:
		// Operator admin can only manage scoped keys in accounts of their operator
		if apiUser.OperatorID == nil || *apiUser.OperatorID != account.OperatorID {
			return fmt.Errorf("%w: operator admin can only manage scoped keys in their operator", ErrPermissionDenied)
		}
		return nil
	case entities.RoleAccountAdmin:
		return fmt.Errorf("%w: account admins cannot manage scoped keys", ErrPermissionDenied)
	default:
		return ErrPermissionDenied
	}
}
