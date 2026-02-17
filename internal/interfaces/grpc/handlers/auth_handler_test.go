package handlers

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type AuthHandlerTestSuite struct {
	suite.Suite
	db          *gorm.DB
	authService *services.AuthService
	handler     *AuthHandler
	adminUser   *entities.APIUser
}

func (s *AuthHandlerTestSuite) SetupTest() {
	// Create in-memory SQLite database
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	s.Require().NoError(err)

	// Run migrations
	err = db.AutoMigrate(&sql.APIUserModel{})
	s.Require().NoError(err)

	s.db = db
	repo := sql.NewAPIUserRepo(db)
	s.authService = services.NewAuthService(repo, "test-jwt-secret-key-32bytes!!!!!", 1*time.Hour)

	// NewAuthHandler returns nisv1connect.AuthServiceHandler, but we know it is *AuthHandler
	s.handler = NewAuthHandler(s.authService).(*AuthHandler)

	// Create an admin user in the database for context-based auth checks
	s.adminUser = s.createAdminUser("admin-user", "admin-password")
}

func (s *AuthHandlerTestSuite) TearDownTest() {
	sqlDB, err := s.db.DB()
	s.Require().NoError(err)
	_ = sqlDB.Close()
}

func TestAuthHandlerSuite(t *testing.T) {
	suite.Run(t, new(AuthHandlerTestSuite))
}

// createAdminUser is a helper that creates an admin user via the service
func (s *AuthHandlerTestSuite) createAdminUser(username, password string) *entities.APIUser {
	ctx := context.Background()
	requestingAdmin := &entities.APIUser{Role: entities.RoleAdmin}
	user, err := s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: username,
		Password: password,
		Role:     entities.RoleAdmin,
	}, requestingAdmin)
	s.Require().NoError(err)
	return user
}

// ctxWithUser returns a context with the given user set in the middleware context key
func ctxWithUser(user *entities.APIUser) context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, user)
}

// --- Login Tests ---

func (s *AuthHandlerTestSuite) TestLogin_ValidCredentials() {
	// Create a user via the service
	ctx := context.Background()
	requestingAdmin := &entities.APIUser{Role: entities.RoleAdmin}
	_, err := s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: "loginuser",
		Password: "correctpassword",
		Role:     entities.RoleAdmin,
	}, requestingAdmin)
	s.Require().NoError(err)

	// Login via the handler
	req := connect.NewRequest(&pb.LoginRequest{
		Username: "loginuser",
		Password: "correctpassword",
	})
	resp, err := s.handler.Login(ctx, req)

	s.NoError(err)
	s.NotNil(resp)
	s.NotEmpty(resp.Msg.Token)
	s.Equal("loginuser", resp.Msg.User.Username)
	s.Contains(resp.Msg.User.Permissions, "admin")
}

func (s *AuthHandlerTestSuite) TestLogin_InvalidCredentials() {
	ctx := context.Background()

	// Create a user via the service
	requestingAdmin := &entities.APIUser{Role: entities.RoleAdmin}
	_, err := s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: "loginuser2",
		Password: "correctpassword",
		Role:     entities.RoleAdmin,
	}, requestingAdmin)
	s.Require().NoError(err)

	// Login with wrong password
	req := connect.NewRequest(&pb.LoginRequest{
		Username: "loginuser2",
		Password: "wrongpassword",
	})
	resp, err := s.handler.Login(ctx, req)

	s.Error(err)
	s.Nil(resp)

	// Should be an Unauthenticated connect error
	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeUnauthenticated, connectErr.Code())
}

func (s *AuthHandlerTestSuite) TestLogin_NonexistentUser() {
	ctx := context.Background()

	req := connect.NewRequest(&pb.LoginRequest{
		Username: "doesnotexist",
		Password: "somepassword",
	})
	resp, err := s.handler.Login(ctx, req)

	s.Error(err)
	s.Nil(resp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeUnauthenticated, connectErr.Code())
}

// --- ValidateToken Tests ---

func (s *AuthHandlerTestSuite) TestValidateToken_ValidToken() {
	ctx := context.Background()

	// Create user and login to get a token
	requestingAdmin := &entities.APIUser{Role: entities.RoleAdmin}
	_, err := s.authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: "tokenuser",
		Password: "tokenpassword",
		Role:     entities.RoleAdmin,
	}, requestingAdmin)
	s.Require().NoError(err)

	loginResp, err := s.authService.Login(ctx, services.LoginRequest{
		Username: "tokenuser",
		Password: "tokenpassword",
	})
	s.Require().NoError(err)

	// Validate the token via the handler
	req := connect.NewRequest(&pb.ValidateTokenRequest{
		Token: loginResp.Token,
	})
	resp, err := s.handler.ValidateToken(ctx, req)

	s.NoError(err)
	s.NotNil(resp)
	s.True(resp.Msg.Valid)
	s.NotNil(resp.Msg.User)
	s.Equal("tokenuser", resp.Msg.User.Username)
}

func (s *AuthHandlerTestSuite) TestValidateToken_InvalidToken() {
	ctx := context.Background()

	req := connect.NewRequest(&pb.ValidateTokenRequest{
		Token: "this-is-not-a-valid-jwt-token",
	})
	resp, err := s.handler.ValidateToken(ctx, req)

	// ValidateToken returns a response with Valid=false, not an error
	s.NoError(err)
	s.NotNil(resp)
	s.False(resp.Msg.Valid)
	s.Nil(resp.Msg.User)
}

// --- CreateAPIUser Tests ---

func (s *AuthHandlerTestSuite) TestCreateAPIUser_Success() {
	ctx := ctxWithUser(s.adminUser)

	req := connect.NewRequest(&pb.CreateAPIUserRequest{
		Username:    "newuser",
		Password:    "newpassword",
		Permissions: []string{"admin"},
	})
	resp, err := s.handler.CreateAPIUser(ctx, req)

	s.NoError(err)
	s.NotNil(resp)
	s.Equal("newuser", resp.Msg.User.Username)
	s.Contains(resp.Msg.User.Permissions, "admin")
	s.NotEmpty(resp.Msg.User.Id)
}

func (s *AuthHandlerTestSuite) TestCreateAPIUser_NoUserInContext() {
	// Call without setting user in context
	ctx := context.Background()

	req := connect.NewRequest(&pb.CreateAPIUserRequest{
		Username:    "newuser",
		Password:    "newpassword",
		Permissions: []string{"admin"},
	})
	resp, err := s.handler.CreateAPIUser(ctx, req)

	s.Error(err)
	s.Nil(resp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeUnauthenticated, connectErr.Code())
}

func (s *AuthHandlerTestSuite) TestCreateAPIUser_DuplicateUsername() {
	ctx := ctxWithUser(s.adminUser)

	// Create user first time
	req := connect.NewRequest(&pb.CreateAPIUserRequest{
		Username:    "dupuser",
		Password:    "password1",
		Permissions: []string{"admin"},
	})
	_, err := s.handler.CreateAPIUser(ctx, req)
	s.NoError(err)

	// Try to create again with same username
	req2 := connect.NewRequest(&pb.CreateAPIUserRequest{
		Username:    "dupuser",
		Password:    "password2",
		Permissions: []string{"admin"},
	})
	resp, err := s.handler.CreateAPIUser(ctx, req2)

	s.Error(err)
	s.Nil(resp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeAlreadyExists, connectErr.Code())
}

// --- ListAPIUsers Tests ---

func (s *AuthHandlerTestSuite) TestListAPIUsers_Success() {
	ctx := ctxWithUser(s.adminUser)

	// Create a couple of extra users via the handler
	for _, name := range []string{"listuser1", "listuser2"} {
		req := connect.NewRequest(&pb.CreateAPIUserRequest{
			Username:    name,
			Password:    "password",
			Permissions: []string{"admin"},
		})
		_, err := s.handler.CreateAPIUser(ctx, req)
		s.Require().NoError(err)
	}

	// List users
	listReq := connect.NewRequest(&pb.ListAPIUsersRequest{})
	resp, err := s.handler.ListAPIUsers(ctx, listReq)

	s.NoError(err)
	s.NotNil(resp)
	// Should have at least the admin user + 2 created users
	s.GreaterOrEqual(len(resp.Msg.Users), 3)
}

func (s *AuthHandlerTestSuite) TestListAPIUsers_NoUserInContext() {
	ctx := context.Background()

	req := connect.NewRequest(&pb.ListAPIUsersRequest{})
	resp, err := s.handler.ListAPIUsers(ctx, req)

	s.Error(err)
	s.Nil(resp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeUnauthenticated, connectErr.Code())
}

// --- DeleteAPIUser Tests ---

func (s *AuthHandlerTestSuite) TestDeleteAPIUser_Success() {
	ctx := ctxWithUser(s.adminUser)

	// Create a user to delete
	createReq := connect.NewRequest(&pb.CreateAPIUserRequest{
		Username:    "deleteuser",
		Password:    "password",
		Permissions: []string{"admin"},
	})
	createResp, err := s.handler.CreateAPIUser(ctx, createReq)
	s.Require().NoError(err)

	userID := createResp.Msg.User.Id

	// Delete the user
	deleteReq := connect.NewRequest(&pb.DeleteAPIUserRequest{
		Id: userID,
	})
	resp, err := s.handler.DeleteAPIUser(ctx, deleteReq)

	s.NoError(err)
	s.NotNil(resp)

	// Verify user is gone by trying to get them
	getReq := connect.NewRequest(&pb.GetAPIUserRequest{
		Id: userID,
	})
	getResp, err := s.handler.GetAPIUser(ctx, getReq)
	s.Error(err)
	s.Nil(getResp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeNotFound, connectErr.Code())
}

func (s *AuthHandlerTestSuite) TestDeleteAPIUser_NotFound() {
	ctx := ctxWithUser(s.adminUser)

	deleteReq := connect.NewRequest(&pb.DeleteAPIUserRequest{
		Id: uuid.New().String(),
	})
	resp, err := s.handler.DeleteAPIUser(ctx, deleteReq)

	s.Error(err)
	s.Nil(resp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeNotFound, connectErr.Code())
}

func (s *AuthHandlerTestSuite) TestDeleteAPIUser_NoUserInContext() {
	ctx := context.Background()

	deleteReq := connect.NewRequest(&pb.DeleteAPIUserRequest{
		Id: uuid.New().String(),
	})
	resp, err := s.handler.DeleteAPIUser(ctx, deleteReq)

	s.Error(err)
	s.Nil(resp)

	connectErr, ok := err.(*connect.Error)
	s.True(ok)
	s.Equal(connect.CodeUnauthenticated, connectErr.Code())
}
