package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// Mock repositories for testing
type mockOperatorRepo struct {
	operators map[uuid.UUID]*entities.Operator
}

func (m *mockOperatorRepo) Create(ctx context.Context, operator *entities.Operator) error {
	m.operators[operator.ID] = operator
	return nil
}

func (m *mockOperatorRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Operator, error) {
	op, ok := m.operators[id]
	if !ok {
		return nil, repositories.ErrNotFound
	}
	return op, nil
}

func (m *mockOperatorRepo) GetByName(ctx context.Context, name string) (*entities.Operator, error) {
	for _, op := range m.operators {
		if op.Name == name {
			return op, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (m *mockOperatorRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Operator, error) {
	result := make([]*entities.Operator, 0, len(m.operators))
	for _, op := range m.operators {
		result = append(result, op)
	}
	return result, nil
}

func (m *mockOperatorRepo) Update(ctx context.Context, operator *entities.Operator) error {
	m.operators[operator.ID] = operator
	return nil
}

func (m *mockOperatorRepo) Delete(ctx context.Context, id uuid.UUID) error {
	delete(m.operators, id)
	return nil
}

func (m *mockOperatorRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error) {
	for _, op := range m.operators {
		if op.PublicKey == publicKey {
			return op, nil
		}
	}
	return nil, repositories.ErrNotFound
}

type mockAccountRepo struct {
	accounts map[uuid.UUID]*entities.Account
}

func (m *mockAccountRepo) Create(ctx context.Context, account *entities.Account) error {
	m.accounts[account.ID] = account
	return nil
}

func (m *mockAccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Account, error) {
	acc, ok := m.accounts[id]
	if !ok {
		return nil, repositories.ErrNotFound
	}
	return acc, nil
}

func (m *mockAccountRepo) GetByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Account, error) {
	for _, acc := range m.accounts {
		if acc.OperatorID == operatorID && acc.Name == name {
			return acc, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (m *mockAccountRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.Account, error) {
	for _, acc := range m.accounts {
		if acc.PublicKey == publicKey {
			return acc, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (m *mockAccountRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Account, error) {
	result := make([]*entities.Account, 0)
	for _, acc := range m.accounts {
		if acc.OperatorID == operatorID {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepo) Update(ctx context.Context, account *entities.Account) error {
	m.accounts[account.ID] = account
	return nil
}

func (m *mockAccountRepo) Delete(ctx context.Context, id uuid.UUID) error {
	delete(m.accounts, id)
	return nil
}

func (m *mockAccountRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Account, error) {
	result := make([]*entities.Account, 0, len(m.accounts))
	for _, acc := range m.accounts {
		result = append(result, acc)
	}
	return result, nil
}

type mockUserRepo struct {
	users map[uuid.UUID]*entities.User
}

func (m *mockUserRepo) Create(ctx context.Context, user *entities.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	user, ok := m.users[id]
	if !ok {
		return nil, repositories.ErrNotFound
	}
	return user, nil
}

func (m *mockUserRepo) GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.User, error) {
	for _, user := range m.users {
		if user.AccountID == accountID && user.Name == name {
			return user, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (m *mockUserRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.User, error) {
	for _, user := range m.users {
		if user.PublicKey == publicKey {
			return user, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (m *mockUserRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	result := make([]*entities.User, 0)
	for _, user := range m.users {
		if user.AccountID == accountID {
			result = append(result, user)
		}
	}
	return result, nil
}

func (m *mockUserRepo) Update(ctx context.Context, user *entities.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	delete(m.users, id)
	return nil
}

func (m *mockUserRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.User, error) {
	result := make([]*entities.User, 0, len(m.users))
	for _, user := range m.users {
		result = append(result, user)
	}
	return result, nil
}

func (m *mockUserRepo) ListByScopedSigningKey(ctx context.Context, scopedKeyID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	result := make([]*entities.User, 0)
	for _, user := range m.users {
		if user.ScopedSigningKeyID != nil && *user.ScopedSigningKeyID == scopedKeyID {
			result = append(result, user)
		}
	}
	return result, nil
}

func (m *mockUserRepo) ListForExpirySweep(ctx context.Context, kind repositories.ExpirySweepKind, now time.Time, warnWindow time.Duration, limit int) ([]*entities.User, error) {
	return nil, nil
}

// Test fixtures
func setupPermissionTest() (*PermissionService, *mockOperatorRepo, *mockAccountRepo, *mockUserRepo, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	operatorRepo := &mockOperatorRepo{operators: make(map[uuid.UUID]*entities.Operator)}
	accountRepo := &mockAccountRepo{accounts: make(map[uuid.UUID]*entities.Account)}
	userRepo := &mockUserRepo{users: make(map[uuid.UUID]*entities.User)}

	permService := NewPermissionService(operatorRepo, accountRepo, userRepo)

	// Create test data
	operator1ID := uuid.New()
	operator2ID := uuid.New()

	operatorRepo.operators[operator1ID] = &entities.Operator{
		ID:   operator1ID,
		Name: "operator1",
	}
	operatorRepo.operators[operator2ID] = &entities.Operator{
		ID:   operator2ID,
		Name: "operator2",
	}

	// Create accounts in each operator
	account1ID := uuid.New()
	account2ID := uuid.New()

	accountRepo.accounts[account1ID] = &entities.Account{
		ID:         account1ID,
		Name:       "account1",
		OperatorID: operator1ID,
	}
	accountRepo.accounts[account2ID] = &entities.Account{
		ID:         account2ID,
		Name:       "account2",
		OperatorID: operator2ID,
	}

	return permService, operatorRepo, accountRepo, userRepo, operator1ID, operator2ID, account1ID, account2ID
}

// Test CanCreateOperator
func TestCanCreateOperator(t *testing.T) {
	permService, _, _, _, _, _, _, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin can create operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin cannot create operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin},
			expectError: true,
		},
		{
			name:        "Account admin cannot create operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanCreateOperator(tt.apiUser)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanReadOperator
func TestCanReadOperator(t *testing.T) {
	permService, _, _, _, operator1ID, operator2ID, _, _ := setupPermissionTest()
	ctx := context.Background()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		operatorID  uuid.UUID
		expectError bool
	}{
		{
			name:        "Admin can read any operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			operatorID:  operator1ID,
			expectError: false,
		},
		{
			name:        "Operator admin can read own operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			operatorID:  operator1ID,
			expectError: false,
		},
		{
			name:        "Operator admin cannot read other operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			operatorID:  operator2ID,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanReadOperator(ctx, tt.apiUser, tt.operatorID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanUpdateOperator
func TestCanUpdateOperator(t *testing.T) {
	permService, _, _, _, operator1ID, _, _, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin can update operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin cannot update operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectError: true,
		},
		{
			name:        "Account admin cannot update operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanUpdateOperator(tt.apiUser, operator1ID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanDeleteOperator
func TestCanDeleteOperator(t *testing.T) {
	permService, _, _, _, operator1ID, _, _, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin can delete operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin cannot delete operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectError: true,
		},
		{
			name:        "Account admin cannot delete operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanDeleteOperator(tt.apiUser, operator1ID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test FilterOperators
func TestFilterOperators(t *testing.T) {
	permService, operatorRepo, _, _, operator1ID, operator2ID, _, _ := setupPermissionTest()
	ctx := context.Background()

	allOperators := []*entities.Operator{
		operatorRepo.operators[operator1ID],
		operatorRepo.operators[operator2ID],
	}

	tests := []struct {
		name           string
		apiUser        *entities.APIUser
		expectedCount  int
		expectedIDs    []uuid.UUID
	}{
		{
			name:          "Admin sees all operators",
			apiUser:       &entities.APIUser{Role: entities.RoleAdmin},
			expectedCount: 2,
			expectedIDs:   []uuid.UUID{operator1ID, operator2ID},
		},
		{
			name:          "Operator admin sees only their operator",
			apiUser:       &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectedCount: 1,
			expectedIDs:   []uuid.UUID{operator1ID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered, err := permService.FilterOperators(ctx, tt.apiUser, allOperators)
			assert.NoError(t, err)
			assert.Len(t, filtered, tt.expectedCount)

			if tt.expectedCount > 0 {
				for _, expectedID := range tt.expectedIDs {
					found := false
					for _, op := range filtered {
						if op.ID == expectedID {
							found = true
							break
						}
					}
					assert.True(t, found, "Expected operator %s not found", expectedID)
				}
			}
		})
	}
}

// Test CanCreateAccount
func TestCanCreateAccount(t *testing.T) {
	permService, _, _, _, operator1ID, operator2ID, _, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		operatorID  uuid.UUID
		expectError bool
	}{
		{
			name:        "Admin can create account in any operator",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			operatorID:  operator1ID,
			expectError: false,
		},
		{
			name:        "Operator admin can create account in own operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			operatorID:  operator1ID,
			expectError: false,
		},
		{
			name:        "Operator admin cannot create account in other operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			operatorID:  operator2ID,
			expectError: true,
		},
		{
			name:        "Account admin cannot create accounts",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin},
			operatorID:  operator1ID,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanCreateAccount(tt.apiUser, tt.operatorID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanReadAccount
func TestCanReadAccount(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, account2ID := setupPermissionTest()
	ctx := context.Background()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		accountID   uuid.UUID
		expectError bool
	}{
		{
			name:        "Admin can read any account",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			accountID:   account1ID,
			expectError: false,
		},
		{
			name:        "Operator admin can read accounts in own operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			accountID:   account1ID,
			expectError: false,
		},
		{
			name:        "Operator admin cannot read accounts in other operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			accountID:   account2ID,
			expectError: true,
		},
		{
			name:        "Account admin can read own account",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			accountID:   account1ID,
			expectError: false,
		},
		{
			name:        "Account admin cannot read other account",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			accountID:   account2ID,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanReadAccount(ctx, tt.apiUser, tt.accountID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanUpdateAccount
func TestCanUpdateAccount(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, account2ID := setupPermissionTest()
	ctx := context.Background()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		accountID   uuid.UUID
		expectError bool
	}{
		{
			name:        "Admin can update any account",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			accountID:   account1ID,
			expectError: false,
		},
		{
			name:        "Operator admin can update accounts in own operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			accountID:   account1ID,
			expectError: false,
		},
		{
			name:        "Operator admin cannot update accounts in other operator",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			accountID:   account2ID,
			expectError: true,
		},
		{
			name:        "Account admin cannot update accounts",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			accountID:   account1ID,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanUpdateAccount(ctx, tt.apiUser, tt.accountID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanDeleteAccount
func TestCanDeleteAccount(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()
	ctx := context.Background()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin can delete accounts",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin cannot delete accounts",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectError: true,
		},
		{
			name:        "Account admin cannot delete accounts",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanDeleteAccount(ctx, tt.apiUser, account1ID)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanManageAPIUsers
func TestCanManageAPIUsers(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin can manage API users",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin cannot manage API users",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectError: true,
		},
		{
			name:        "Account admin cannot manage API users",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanManageAPIUsers(tt.apiUser)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanReadEvents
func TestCanReadEvents_AdminAllowed_OthersDenied(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin allowed",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin denied",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectError: true,
		},
		{
			name:        "Account admin denied",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanReadEvents(tt.apiUser)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test CanCreateWebhookSubscription
func TestCanCreateWebhookSubscription_AdminAlwaysAllowed(t *testing.T) {
	permService, _, _, _, operator1ID, _, _, _ := setupPermissionTest()
	ctx := context.Background()

	err := permService.CanCreateWebhookSubscription(ctx, &entities.APIUser{Role: entities.RoleAdmin}, operator1ID)
	assert.NoError(t, err)
}

func TestCanCreateWebhookSubscription_OperatorAdminOwnOp_Allowed(t *testing.T) {
	permService, _, _, _, operator1ID, _, _, _ := setupPermissionTest()
	ctx := context.Background()

	err := permService.CanCreateWebhookSubscription(ctx,
		&entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
		operator1ID)
	assert.NoError(t, err)
}

func TestCanCreateWebhookSubscription_OperatorAdminOtherOp_Denied(t *testing.T) {
	permService, _, _, _, operator1ID, operator2ID, _, _ := setupPermissionTest()
	ctx := context.Background()

	err := permService.CanCreateWebhookSubscription(ctx,
		&entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
		operator2ID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionDenied)
}

func TestCanCreateWebhookSubscription_AccountAdmin_Denied(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()
	ctx := context.Background()

	err := permService.CanCreateWebhookSubscription(ctx,
		&entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
		operator1ID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionDenied)
}

// Test CanCreateCluster
func TestCanCreateCluster(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()

	tests := []struct {
		name        string
		apiUser     *entities.APIUser
		expectError bool
	}{
		{
			name:        "Admin can create clusters",
			apiUser:     &entities.APIUser{Role: entities.RoleAdmin},
			expectError: false,
		},
		{
			name:        "Operator admin cannot create clusters",
			apiUser:     &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			expectError: true,
		},
		{
			name:        "Account admin cannot create clusters",
			apiUser:     &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanCreateCluster(tt.apiUser)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// P2 — permission tests for revocation, JWT policy, sweep, and regen
// ---------------------------------------------------------------------------

// TestCanRevokeUser_AdminOK verifies that a global admin can revoke any user.
func TestCanRevokeUser_AdminOK(t *testing.T) {
	permService, _, _, userRepo, _, _, _, _ := setupPermissionTest()
	ctx := context.Background()

	userID := uuid.New()
	userRepo.users[userID] = &entities.User{ID: userID, AccountID: uuid.New()}

	admin := &entities.APIUser{Role: entities.RoleAdmin}
	err := permService.CanRevokeUser(ctx, admin, userID)
	assert.NoError(t, err, "admin must be able to revoke any user")
}

// TestCanRevokeUser_OperatorAdminOwnOK verifies that an operator-admin can
// revoke a user that belongs to an account in their operator.
func TestCanRevokeUser_OperatorAdminOwnOK(t *testing.T) {
	permService, _, _, userRepo, operator1ID, _, account1ID, _ := setupPermissionTest()
	ctx := context.Background()

	userID := uuid.New()
	userRepo.users[userID] = &entities.User{ID: userID, AccountID: account1ID}

	opAdmin := &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID}
	err := permService.CanRevokeUser(ctx, opAdmin, userID)
	assert.NoError(t, err, "operator-admin must be able to revoke users in their own operator")
}

// TestCanRevokeUser_OperatorAdminOtherDenied verifies that an operator-admin
// cannot revoke a user that belongs to a different operator.
func TestCanRevokeUser_OperatorAdminOtherDenied(t *testing.T) {
	permService, _, _, userRepo, _, _, _, account2ID := setupPermissionTest()
	ctx := context.Background()

	userID := uuid.New()
	// account2 belongs to operator2; a different operator1-admin should be denied.
	userRepo.users[userID] = &entities.User{ID: userID, AccountID: account2ID}

	// Use a fresh UUID that isn't in the fixture operators so ownsAccount returns false.
	strangerOpID := uuid.New()
	opAdmin := &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &strangerOpID}
	err := permService.CanRevokeUser(ctx, opAdmin, userID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionDenied,
		"operator-admin must not revoke users in another operator")
}

// TestCanRevokeUser_AccountAdminDenied verifies that an account-admin can never
// revoke users.
func TestCanRevokeUser_AccountAdminDenied(t *testing.T) {
	permService, _, _, userRepo, _, _, account1ID, _ := setupPermissionTest()
	ctx := context.Background()

	userID := uuid.New()
	userRepo.users[userID] = &entities.User{ID: userID, AccountID: account1ID}

	accAdmin := &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID}
	err := permService.CanRevokeUser(ctx, accAdmin, userID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionDenied, "account-admin must not be allowed to revoke users")
}

// TestCanSetOperatorJWTPolicy_AdminOnly verifies that only admin can set the
// JWT lifecycle policy on an operator.
func TestCanSetOperatorJWTPolicy_AdminOnly(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()

	tests := []struct {
		name      string
		apiUser   *entities.APIUser
		wantAllow bool
	}{
		{"admin", &entities.APIUser{Role: entities.RoleAdmin}, true},
		{"operator-admin", &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID}, false},
		{"account-admin", &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanSetOperatorJWTPolicy(tt.apiUser, operator1ID)
			if tt.wantAllow {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			}
		})
	}
}

// TestCanRunJWTExpirySweep_AdminOnly verifies that only admin can trigger a
// manual sweep.
func TestCanRunJWTExpirySweep_AdminOnly(t *testing.T) {
	permService, _, _, _, operator1ID, _, account1ID, _ := setupPermissionTest()

	tests := []struct {
		name      string
		apiUser   *entities.APIUser
		wantAllow bool
	}{
		{"admin", &entities.APIUser{Role: entities.RoleAdmin}, true},
		{"operator-admin", &entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID}, false},
		{"account-admin", &entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := permService.CanRunJWTExpirySweep(tt.apiUser)
			if tt.wantAllow {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			}
		})
	}
}

// TestCanRegenerateUserCredentials_FollowsCanUpdateUser verifies that
// CanRegenerateUserCredentials delegates to CanUpdateUser semantics: any role
// that owns the account can regenerate credentials (including account-admin).
func TestCanRegenerateUserCredentials_FollowsCanUpdateUser(t *testing.T) {
	permService, _, _, userRepo, operator1ID, _, account1ID, account2ID := setupPermissionTest()
	ctx := context.Background()

	userInAcc1 := uuid.New()
	userRepo.users[userInAcc1] = &entities.User{ID: userInAcc1, AccountID: account1ID}

	cases := []struct {
		name      string
		caller    *entities.APIUser
		wantAllow bool
	}{
		{
			"admin can regen any user",
			&entities.APIUser{Role: entities.RoleAdmin},
			true,
		},
		{
			"operator-admin in own operator can regen",
			&entities.APIUser{Role: entities.RoleOperatorAdmin, OperatorID: &operator1ID},
			true,
		},
		{
			"account-admin in own account can regen (unlike revoke)",
			&entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account1ID},
			true,
		},
		{
			"account-admin in other account denied",
			&entities.APIUser{Role: entities.RoleAccountAdmin, AccountID: &account2ID},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := permService.CanRegenerateUserCredentials(ctx, tc.caller, userInAcc1)
			if tc.wantAllow {
				assert.NoError(t, err, tc.name)
			} else {
				assert.Error(t, err, tc.name)
				assert.ErrorIs(t, err, ErrPermissionDenied)
			}
		})
	}
}
