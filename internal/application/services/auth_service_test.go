package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type AuthServiceTestSuite struct {
	suite.Suite
	db          *gorm.DB
	repo        repositories.APIUserRepository
	authService *AuthService
}

func (s *AuthServiceTestSuite) SetupTest() {
	// Create in-memory SQLite database
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	s.Require().NoError(err)

	// Run migrations
	err = db.AutoMigrate(
		&entities.APIUser{},
	)
	s.Require().NoError(err)

	s.db = db
	s.repo = sql.NewAPIUserRepo(db)
	s.authService = NewAuthService(s.repo, "test-secret-key-for-jwt-signing", 1*time.Hour)
}

func (s *AuthServiceTestSuite) TearDownTest() {
	sqlDB, err := s.db.DB()
	s.Require().NoError(err)
	sqlDB.Close()
}

func TestAuthServiceSuite(t *testing.T) {
	suite.Run(t, new(AuthServiceTestSuite))
}

func (s *AuthServiceTestSuite) TestCreateAPIUser() {
	ctx := context.Background()

	// Create an API user
	user, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})

	s.NoError(err)
	s.NotNil(user)
	s.Equal("testuser", user.Username)
	s.Equal(entities.RoleAdmin, user.Role)
	s.NotEmpty(user.PasswordHash)
	s.NotEqual("testpassword", user.PasswordHash) // Password should be hashed
}

func (s *AuthServiceTestSuite) TestCreateAPIUser_DuplicateUsername() {
	ctx := context.Background()

	// Create first user
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	// Try to create second user with same username
	_, err = s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "differentpassword",
		Role:     entities.RoleOperatorAdmin,
	})
	s.Error(err)
	s.Equal(repositories.ErrAlreadyExists, err)
}

func (s *AuthServiceTestSuite) TestLogin_Success() {
	ctx := context.Background()

	// Create a user
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	// Login with correct credentials
	resp, err := s.authService.Login(ctx, LoginRequest{
		Username: "testuser",
		Password: "testpassword",
	})

	s.NoError(err)
	s.NotNil(resp)
	s.NotEmpty(resp.Token)
	s.Equal("testuser", resp.User.Username)
	s.Equal(entities.RoleAdmin, resp.User.Role)
}

func (s *AuthServiceTestSuite) TestLogin_InvalidPassword() {
	ctx := context.Background()

	// Create a user
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	// Login with wrong password
	_, err = s.authService.Login(ctx, LoginRequest{
		Username: "testuser",
		Password: "wrongpassword",
	})

	s.Error(err)
	s.Contains(err.Error(), "invalid username or password")
}

func (s *AuthServiceTestSuite) TestLogin_NonexistentUser() {
	ctx := context.Background()

	// Try to login with nonexistent user
	_, err := s.authService.Login(ctx, LoginRequest{
		Username: "nonexistent",
		Password: "testpassword",
	})

	s.Error(err)
	s.Contains(err.Error(), "invalid username or password")
}

func (s *AuthServiceTestSuite) TestValidateToken_Success() {
	ctx := context.Background()

	// Create a user and login
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	loginResp, err := s.authService.Login(ctx, LoginRequest{
		Username: "testuser",
		Password: "testpassword",
	})
	s.NoError(err)

	// Validate the token
	user, err := s.authService.ValidateToken(ctx, loginResp.Token)

	s.NoError(err)
	s.NotNil(user)
	s.Equal("testuser", user.Username)
	s.Equal(entities.RoleAdmin, user.Role)
}

func (s *AuthServiceTestSuite) TestValidateToken_InvalidToken() {
	ctx := context.Background()

	// Try to validate an invalid token
	_, err := s.authService.ValidateToken(ctx, "invalid-token")

	s.Error(err)
	s.Contains(err.Error(), "invalid token")
}

func (s *AuthServiceTestSuite) TestValidateToken_ExpiredToken() {
	ctx := context.Background()

	// Create a service with very short TTL
	shortTTLService := NewAuthService(s.repo, "test-secret-key-for-jwt-signing", 1*time.Millisecond)

	// Create a user and login
	_, err := shortTTLService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	loginResp, err := shortTTLService.Login(ctx, LoginRequest{
		Username: "testuser",
		Password: "testpassword",
	})
	s.NoError(err)

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	// Try to validate the expired token
	_, err = shortTTLService.ValidateToken(ctx, loginResp.Token)

	s.Error(err)
	s.Contains(err.Error(), "invalid token")
}

func (s *AuthServiceTestSuite) TestUpdateAPIUserPassword() {
	ctx := context.Background()

	// Create a user
	user, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "oldpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	oldPasswordHash := user.PasswordHash

	// Update password
	updatedUser, err := s.authService.UpdateAPIUserPassword(ctx, user.ID, UpdatePasswordRequest{
		Password: "newpassword",
	})

	s.NoError(err)
	s.NotEqual(oldPasswordHash, updatedUser.PasswordHash)

	// Try to login with old password (should fail)
	_, err = s.authService.Login(ctx, LoginRequest{
		Username: "testuser",
		Password: "oldpassword",
	})
	s.Error(err)

	// Try to login with new password (should succeed)
	resp, err := s.authService.Login(ctx, LoginRequest{
		Username: "testuser",
		Password: "newpassword",
	})
	s.NoError(err)
	s.NotNil(resp)
}

func (s *AuthServiceTestSuite) TestUpdateAPIUserRole() {
	ctx := context.Background()

	// Create a user
	user, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleOperatorAdmin,
	})
	s.NoError(err)
	s.Equal(entities.RoleOperatorAdmin, user.Role)

	// Update role to admin
	updatedUser, err := s.authService.UpdateAPIUserRole(ctx, user.ID, UpdateRoleRequest{
		Role: entities.RoleAdmin,
	})

	s.NoError(err)
	s.Equal(entities.RoleAdmin, updatedUser.Role)
}

func (s *AuthServiceTestSuite) TestDeleteAPIUser() {
	ctx := context.Background()

	// Create a user
	user, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "testuser",
		Password: "testpassword",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	// Delete the user
	err = s.authService.DeleteAPIUser(ctx, user.ID)
	s.NoError(err)

	// Try to get the deleted user
	_, err = s.authService.GetAPIUser(ctx, user.ID)
	s.Error(err)
	s.Equal(repositories.ErrNotFound, err)
}

func (s *AuthServiceTestSuite) TestListAPIUsers() {
	ctx := context.Background()

	// Create multiple users
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "user1",
		Password: "password1",
		Role:     entities.RoleAdmin,
	})
	s.NoError(err)

	_, err = s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "user2",
		Password: "password2",
		Role:     entities.RoleOperatorAdmin,
	})
	s.NoError(err)

	// List all users
	users, err := s.authService.ListAPIUsers(ctx)
	s.NoError(err)
	s.Len(users, 2)
}
