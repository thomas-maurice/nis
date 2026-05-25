package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

type RBACIsolationTestSuite struct {
	suite.Suite
	repoFactory persistence.RepositoryFactory

	// Services
	operatorService *services.OperatorService
	accountService  *services.AccountService
	userService     *services.UserService
	authService     *services.AuthService
	permService     *services.PermissionService
	jwtService      *services.JWTService
	encryptor       encryption.Encryptor

	// Test data - operators
	operator1 *entities.Operator
	operator2 *entities.Operator

	// Test data - accounts
	operator1Account1 *entities.Account
	operator1Account2 *entities.Account
	operator2Account1 *entities.Account

	// Test data - API users
	adminUser         *entities.APIUser
	operator1Admin    *entities.APIUser
	operator2Admin    *entities.APIUser
	account1Admin     *entities.APIUser
	account2Admin     *entities.APIUser
}

func TestRBACIsolationTestSuite(t *testing.T) {
	suite.Run(t, new(RBACIsolationTestSuite))
}

func (s *RBACIsolationTestSuite) SetupSuite() {
	ctx := context.Background()

	// Create in-memory database
	repoFactory, err := persistence.NewRepositoryFactory(persistence.Config{
		Driver:       "sqlite",
		DSN:          ":memory:",
		MigrationDir: "../../migrations",
	})
	s.Require().NoError(err)
	s.repoFactory = repoFactory

	// Connect to database
	err = repoFactory.Connect(ctx)
	s.Require().NoError(err)

	// Run migrations
	err = repoFactory.Migrate(ctx)
	s.Require().NoError(err)

	// Initialize encryption (needs exactly 32 bytes)
	keys := map[string]string{
		"default": "dGVzdC1lbmNyeXB0aW9uLWtleS1leGFjdGx5LTMyISE=", // "test-encryption-key-exactly-32!!" base64 encoded (32 bytes)
	}
	encryptor, err := encryption.NewChaChaEncryptor(keys, "default")
	s.Require().NoError(err)
	s.encryptor = encryptor

	// Initialize JWT service
	s.jwtService = services.NewJWTService(encryptor)

	// Initialize business services
	// Create accountService first (required by operatorService)
	s.accountService = services.NewAccountService(repoFactory, s.jwtService, encryptor)

	s.operatorService = services.NewOperatorService(repoFactory, s.accountService, s.jwtService, encryptor)

	s.userService = services.NewUserService(
		repoFactory.UserRepository(),
		repoFactory.AccountRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		repoFactory.OperatorRepository(),
		s.jwtService,
		encryptor,
	)

	s.authService = services.NewAuthService(
		repoFactory,
		"test-jwt-secret-key-32-bytes!!",
		24*time.Hour,
	)

	s.permService = services.NewPermissionService(
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
	)
}

func (s *RBACIsolationTestSuite) SetupTest() {
	ctx := context.Background()

	// Create admin API user
	var err error
	s.adminUser, err = s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: "admin",
		Password: "admin-password",
		Role:     entities.RoleAdmin,
	}, &entities.APIUser{Role: entities.RoleAdmin}) // Bootstrap with admin
	s.Require().NoError(err)

	// Create two operators
	s.operator1, err = s.operatorService.CreateOperator(ctx, services.CreateOperatorRequest{
		Name:        "operator1",
		Description: "First operator",
	})
	s.Require().NoError(err)

	s.operator2, err = s.operatorService.CreateOperator(ctx, services.CreateOperatorRequest{
		Name:        "operator2",
		Description: "Second operator",
	})
	s.Require().NoError(err)

	// Create accounts in operator1
	s.operator1Account1, err = s.accountService.CreateAccount(ctx, services.CreateAccountRequest{
		OperatorID:  s.operator1.ID,
		Name:        "op1-account1",
		Description: "Operator 1 - Account 1",
	})
	s.Require().NoError(err)

	s.operator1Account2, err = s.accountService.CreateAccount(ctx, services.CreateAccountRequest{
		OperatorID:  s.operator1.ID,
		Name:        "op1-account2",
		Description: "Operator 1 - Account 2",
	})
	s.Require().NoError(err)

	// Create account in operator2
	s.operator2Account1, err = s.accountService.CreateAccount(ctx, services.CreateAccountRequest{
		OperatorID:  s.operator2.ID,
		Name:        "op2-account1",
		Description: "Operator 2 - Account 1",
	})
	s.Require().NoError(err)

	// Create operator-admin API users
	s.operator1Admin, err = s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username:   "operator1-admin",
		Password:   "op1-password",
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &s.operator1.ID,
	}, s.adminUser)
	s.Require().NoError(err)

	s.operator2Admin, err = s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username:   "operator2-admin",
		Password:   "op2-password",
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &s.operator2.ID,
	}, s.adminUser)
	s.Require().NoError(err)

	// Create account-admin API users
	s.account1Admin, err = s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username:  "account1-admin",
		Password:  "acc1-password",
		Role:      entities.RoleAccountAdmin,
		AccountID: &s.operator1Account1.ID,
	}, s.adminUser)
	s.Require().NoError(err)

	s.account2Admin, err = s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username:  "account2-admin",
		Password:  "acc2-password",
		Role:      entities.RoleAccountAdmin,
		AccountID: &s.operator1Account2.ID,
	}, s.adminUser)
	s.Require().NoError(err)
}

func (s *RBACIsolationTestSuite) TearDownTest() {
	// Clean up test data
	ctx := context.Background()

	// Clean up API users
	if s.adminUser != nil {
		_ = s.authService.DeleteAPIUser(ctx, s.adminUser.ID, s.adminUser)
	}
	if s.operator1Admin != nil {
		_ = s.authService.DeleteAPIUser(ctx, s.operator1Admin.ID, s.adminUser)
	}
	if s.operator2Admin != nil {
		_ = s.authService.DeleteAPIUser(ctx, s.operator2Admin.ID, s.adminUser)
	}
	if s.account1Admin != nil {
		_ = s.authService.DeleteAPIUser(ctx, s.account1Admin.ID, s.adminUser)
	}
	if s.account2Admin != nil {
		_ = s.authService.DeleteAPIUser(ctx, s.account2Admin.ID, s.adminUser)
	}

	// Clean up operators (cascades to accounts)
	if s.operator1 != nil {
		_ = s.operatorService.DeleteOperator(ctx, s.operator1.ID)
	}
	if s.operator2 != nil {
		_ = s.operatorService.DeleteOperator(ctx, s.operator2.ID)
	}
}

func (s *RBACIsolationTestSuite) TearDownSuite() {
	if s.repoFactory != nil {
		_ = s.repoFactory.Close()
	}
}

// Test operator isolation
func (s *RBACIsolationTestSuite) TestOperatorAdmin_CannotReadOtherOperators() {
	ctx := context.Background()

	// Operator1Admin can read operator1
	err := s.permService.CanReadOperator(ctx, s.operator1Admin, s.operator1.ID)
	s.NoError(err, "Operator1Admin should be able to read their own operator")

	// Operator1Admin CANNOT read operator2
	err = s.permService.CanReadOperator(ctx, s.operator1Admin, s.operator2.ID)
	s.Error(err, "Operator1Admin should NOT be able to read operator2")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

func (s *RBACIsolationTestSuite) TestOperatorAdmin_CannotCreateOperators() {
	// Operator admin cannot create operators
	err := s.permService.CanCreateOperator(s.operator1Admin)
	s.Error(err, "Operator admin should NOT be able to create operators")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")

	// Admin CAN create operators
	err = s.permService.CanCreateOperator(s.adminUser)
	s.NoError(err, "Admin should be able to create operators")
}

func (s *RBACIsolationTestSuite) TestOperatorAdmin_CannotUpdateOrDeleteOperators() {
	// Operator admin cannot update even their own operator
	err := s.permService.CanUpdateOperator(s.operator1Admin, s.operator1.ID)
	s.Error(err, "Operator admin should NOT be able to update operators")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")

	// Operator admin cannot delete operators
	err = s.permService.CanDeleteOperator(s.operator1Admin, s.operator1.ID)
	s.Error(err, "Operator admin should NOT be able to delete operators")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

// Test account isolation
func (s *RBACIsolationTestSuite) TestOperatorAdmin_CanOnlyAccessAccountsInTheirOperator() {
	ctx := context.Background()

	// Operator1Admin CAN read accounts in operator1
	err := s.permService.CanReadAccount(ctx, s.operator1Admin, s.operator1Account1.ID)
	s.NoError(err, "Operator1Admin should read accounts in their operator")

	err = s.permService.CanReadAccount(ctx, s.operator1Admin, s.operator1Account2.ID)
	s.NoError(err, "Operator1Admin should read accounts in their operator")

	// Operator1Admin CANNOT read accounts in operator2
	err = s.permService.CanReadAccount(ctx, s.operator1Admin, s.operator2Account1.ID)
	s.Error(err, "Operator1Admin should NOT read accounts in operator2")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

func (s *RBACIsolationTestSuite) TestOperatorAdmin_CanCreateAccountsInTheirOperator() {
	// Operator1Admin CAN create accounts in operator1
	err := s.permService.CanCreateAccount(s.operator1Admin, s.operator1.ID)
	s.NoError(err, "Operator1Admin should create accounts in their operator")

	// Operator1Admin CANNOT create accounts in operator2
	err = s.permService.CanCreateAccount(s.operator1Admin, s.operator2.ID)
	s.Error(err, "Operator1Admin should NOT create accounts in operator2")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

// Test account admin isolation
func (s *RBACIsolationTestSuite) TestAccountAdmin_CannotCreateAccounts() {
	// Account admin cannot create accounts
	err := s.permService.CanCreateAccount(s.account1Admin, s.operator1.ID)
	s.Error(err, "Account admin should NOT be able to create accounts")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

func (s *RBACIsolationTestSuite) TestAccountAdmin_CanOnlyReadTheirAccount() {
	ctx := context.Background()

	// Account1Admin CAN read their account
	err := s.permService.CanReadAccount(ctx, s.account1Admin, s.operator1Account1.ID)
	s.NoError(err, "Account1Admin should read their own account")

	// Account1Admin CANNOT read other accounts
	err = s.permService.CanReadAccount(ctx, s.account1Admin, s.operator1Account2.ID)
	s.Error(err, "Account1Admin should NOT read account2")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")

	err = s.permService.CanReadAccount(ctx, s.account1Admin, s.operator2Account1.ID)
	s.Error(err, "Account1Admin should NOT read operator2 account")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

func (s *RBACIsolationTestSuite) TestAccountAdmin_CannotUpdateOrDeleteAccounts() {
	ctx := context.Background()

	// Account admin cannot update even their own account
	err := s.permService.CanUpdateAccount(ctx, s.account1Admin, s.operator1Account1.ID)
	s.Error(err, "Account admin should NOT update accounts")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")

	// Account admin cannot delete accounts
	err = s.permService.CanDeleteAccount(ctx, s.account1Admin, s.operator1Account1.ID)
	s.Error(err, "Account admin should NOT delete accounts")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

// Test user isolation
func (s *RBACIsolationTestSuite) TestAccountAdmin_CanCreateUsersInTheirAccount() {
	ctx := context.Background()

	// Create a user in account1
	user1, err := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator1Account1.ID,
		Name:      "test-user",
	})
	s.NoError(err)
	defer func() { _ = s.userService.DeleteUser(ctx, user1.ID) }()

	// Account1Admin CAN read users in their account
	err = s.permService.CanReadUser(ctx, s.account1Admin, user1.ID)
	s.NoError(err, "Account1Admin should read users in their account")

	// Create a user in account2
	user2, err := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator1Account2.ID,
		Name:      "test-user2",
	})
	s.NoError(err)
	defer func() { _ = s.userService.DeleteUser(ctx, user2.ID) }()

	// Account1Admin CANNOT read users in account2
	err = s.permService.CanReadUser(ctx, s.account1Admin, user2.ID)
	s.Error(err, "Account1Admin should NOT read users in account2")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

func (s *RBACIsolationTestSuite) TestOperatorAdmin_CanAccessAllUsersInTheirOperator() {
	ctx := context.Background()

	// Create users in different accounts of operator1
	user1, err := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator1Account1.ID,
		Name:      "user-in-account1",
	})
	s.NoError(err)
	defer func() { _ = s.userService.DeleteUser(ctx, user1.ID) }()

	user2, err := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator1Account2.ID,
		Name:      "user-in-account2",
	})
	s.NoError(err)
	defer func() { _ = s.userService.DeleteUser(ctx, user2.ID) }()

	// Create user in operator2
	user3, err := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator2Account1.ID,
		Name:      "user-in-operator2",
	})
	s.NoError(err)
	defer func() { _ = s.userService.DeleteUser(ctx, user3.ID) }()

	// Operator1Admin CAN read users in both operator1 accounts
	err = s.permService.CanReadUser(ctx, s.operator1Admin, user1.ID)
	s.NoError(err, "Operator1Admin should read users in account1")

	err = s.permService.CanReadUser(ctx, s.operator1Admin, user2.ID)
	s.NoError(err, "Operator1Admin should read users in account2")

	// Operator1Admin CANNOT read users in operator2
	err = s.permService.CanReadUser(ctx, s.operator1Admin, user3.ID)
	s.Error(err, "Operator1Admin should NOT read users in operator2")
	s.True(errors.Is(err, services.ErrPermissionDenied), "Error should be ErrPermissionDenied")
}

// Test API user management isolation
func (s *RBACIsolationTestSuite) TestOnlyAdminCanManageAPIUsers() {
	ctx := context.Background()

	// Admin CAN list API users
	users, err := s.authService.ListAPIUsers(ctx, s.adminUser)
	s.NoError(err)
	s.NotEmpty(users, "Admin should see API users")

	// Operator admin CANNOT list API users
	users, err = s.authService.ListAPIUsers(ctx, s.operator1Admin)
	s.Error(err, "Operator admin should NOT list API users")
	s.Nil(users)

	// Account admin CANNOT list API users
	users, err = s.authService.ListAPIUsers(ctx, s.account1Admin)
	s.Error(err, "Account admin should NOT list API users")
	s.Nil(users)

	// Only admin can create API users
	_, err = s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: "test-api-user",
		Password: "password",
		Role:     entities.RoleOperatorAdmin,
		OperatorID: &s.operator1.ID,
	}, s.operator1Admin)
	s.Error(err, "Operator admin should NOT create API users")

	// Admin CAN create API users
	newUser, err := s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: "test-api-user",
		Password: "password",
		Role:     entities.RoleOperatorAdmin,
		OperatorID: &s.operator1.ID,
	}, s.adminUser)
	s.NoError(err, "Admin should create API users")
	if newUser != nil {
		defer func() { _ = s.authService.DeleteAPIUser(ctx, newUser.ID, s.adminUser) }()
	}
}

// Per-row CanReadUser coverage (replaces the old TestCompleteIsolationScenario
// which exercised the deleted PermissionService.FilterUsers helper). The
// list-narrowing equivalent now lives at the SQL layer in
// internal/infrastructure/persistence/sql/listpage_test.go (covers ListPage)
// and search_test.go (covers Search). Per-row authority is what's worth
// pinning here.
func (s *RBACIsolationTestSuite) TestCompleteIsolationScenario_PerRowCanReadUser() {
	ctx := context.Background()

	user1, _ := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator1Account1.ID,
		Name:      "user1-account1",
	})
	defer func() { _ = s.userService.DeleteUser(ctx, user1.ID) }()

	user2, _ := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator1Account2.ID,
		Name:      "user2-account2",
	})
	defer func() { _ = s.userService.DeleteUser(ctx, user2.ID) }()

	user3, _ := s.userService.CreateUser(ctx, services.CreateUserRequest{
		AccountID: s.operator2Account1.ID,
		Name:      "user3-operator2",
	})
	defer func() { _ = s.userService.DeleteUser(ctx, user3.ID) }()

	// Account1Admin: only their account's user.
	s.NoError(s.permService.CanReadUser(ctx, s.account1Admin, user1.ID))
	s.Error(s.permService.CanReadUser(ctx, s.account1Admin, user2.ID))
	s.Error(s.permService.CanReadUser(ctx, s.account1Admin, user3.ID))

	// Operator1Admin: both accounts in operator1, never operator2.
	s.NoError(s.permService.CanReadUser(ctx, s.operator1Admin, user1.ID))
	s.NoError(s.permService.CanReadUser(ctx, s.operator1Admin, user2.ID))
	s.Error(s.permService.CanReadUser(ctx, s.operator1Admin, user3.ID))

	// Operator2Admin: only operator2's user.
	s.Error(s.permService.CanReadUser(ctx, s.operator2Admin, user1.ID))
	s.Error(s.permService.CanReadUser(ctx, s.operator2Admin, user2.ID))
	s.NoError(s.permService.CanReadUser(ctx, s.operator2Admin, user3.ID))

	// Admin: everything.
	s.NoError(s.permService.CanReadUser(ctx, s.adminUser, user1.ID))
	s.NoError(s.permService.CanReadUser(ctx, s.adminUser, user2.ID))
	s.NoError(s.permService.CanReadUser(ctx, s.adminUser, user3.ID))
}

// Test that permissions persist across different operations
func (s *RBACIsolationTestSuite) TestPermissionConsistencyAcrossOperations() {
	ctx := context.Background()

	// Verify operator1Admin consistently cannot access operator2 resources

	// Cannot read operator2
	err := s.permService.CanReadOperator(ctx, s.operator1Admin, s.operator2.ID)
	s.Error(err)

	// Cannot update operator2
	err = s.permService.CanUpdateOperator(s.operator1Admin, s.operator2.ID)
	s.Error(err)

	// Cannot delete operator2
	err = s.permService.CanDeleteOperator(s.operator1Admin, s.operator2.ID)
	s.Error(err)

	// Cannot read accounts in operator2
	err = s.permService.CanReadAccount(ctx, s.operator1Admin, s.operator2Account1.ID)
	s.Error(err)

	// Cannot create accounts in operator2
	err = s.permService.CanCreateAccount(s.operator1Admin, s.operator2.ID)
	s.Error(err)

	s.T().Log("✓ Perfect isolation verified: operator1Admin has NO access to operator2")
}
