package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"golang.org/x/crypto/bcrypt"
)

// AuthService handles authentication and authorization
type AuthService struct {
	factory   persistence.RepositoryFactory
	jwtSecret []byte
	tokenTTL  time.Duration
}

// AuthClaims represents JWT claims for authentication tokens
type AuthClaims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// NewAuthService creates a new AuthService
func NewAuthService(
	factory persistence.RepositoryFactory,
	jwtSecret string,
	tokenTTL time.Duration,
) *AuthService {
	if tokenTTL == 0 {
		tokenTTL = 24 * time.Hour // Default to 24 hours
	}
	return &AuthService{
		factory:   factory,
		jwtSecret: []byte(jwtSecret),
		tokenTTL:  tokenTTL,
	}
}

// LoginRequest contains login credentials
type LoginRequest struct {
	Username string
	Password string
}

// LoginResponse contains the authentication token and user info
type LoginResponse struct {
	Token string
	User  *entities.APIUser
}

// Login authenticates a user and returns a JWT token
func (s *AuthService) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if req.Password == "" {
		return nil, fmt.Errorf("password is required")
	}

	// Get user by username
	user, err := s.factory.APIUserRepository().GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, fmt.Errorf("invalid username or password")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Verify password
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password))
	if err != nil {
		return nil, fmt.Errorf("invalid username or password")
	}

	// Generate JWT token
	token, err := s.generateToken(user)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return &LoginResponse{
		Token: token,
		User:  user,
	}, nil
}

// ValidateToken validates a JWT token and returns the user
func (s *AuthService) ValidateToken(ctx context.Context, tokenString string) (*entities.APIUser, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("token is required")
	}

	// Parse and validate token
	token, err := jwt.ParseWithClaims(tokenString, &AuthClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*AuthClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Get user from database to ensure it still exists
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID in token: %w", err)
	}

	user, err := s.factory.APIUserRepository().GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

// CreateAPIUserRequest contains data for creating an API user
type CreateAPIUserRequest struct {
	Username   string
	Password   string
	Role       entities.APIUserRole
	OperatorID *uuid.UUID // Required for operator-admin role
	AccountID  *uuid.UUID // Required for account-admin role
}

// CreateAPIUser creates a new API user (admin only)
// Requires the requesting user to be passed in context for authorization
func (s *AuthService) CreateAPIUser(ctx context.Context, req CreateAPIUserRequest, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can create API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can create API users")
	}

	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if req.Password == "" {
		return nil, fmt.Errorf("password is required")
	}
	if !req.Role.IsValid() {
		return nil, fmt.Errorf("invalid role: %s", req.Role)
	}

	// Validate role-specific requirements
	switch req.Role {
	case entities.RoleOperatorAdmin:
		if req.OperatorID == nil {
			return nil, fmt.Errorf("operator_id is required for operator-admin role")
		}
		if req.AccountID != nil {
			return nil, fmt.Errorf("account_id must not be set for operator-admin role")
		}
	case entities.RoleAccountAdmin:
		if req.AccountID == nil {
			return nil, fmt.Errorf("account_id is required for account-admin role")
		}
		if req.OperatorID != nil {
			return nil, fmt.Errorf("operator_id must not be set for account-admin role")
		}
	case entities.RoleAdmin:
		if req.OperatorID != nil || req.AccountID != nil {
			return nil, fmt.Errorf("operator_id and account_id must not be set for admin role")
		}
	}

	// Check if username already exists
	existing, err := s.factory.APIUserRepository().GetByUsername(ctx, req.Username)
	if err == nil && existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// Hash password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	var result *entities.APIUser
	err = s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user := &entities.APIUser{
			ID:           uuid.New(),
			Username:     req.Username,
			PasswordHash: string(passwordHash),
			Role:         req.Role,
			OperatorID:   req.OperatorID,
			AccountID:    req.AccountID,
			CreatedAt:    clock.Now(),
			UpdatedAt:    clock.Now(),
		}
		if err := tx.APIUserRepository().Create(ctx, user); err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAPIUserCreated,
			ResourceType: "api_user",
			ResourceID:   user.ID.String(),
			Payload: map[string]any{
				"username": user.Username,
				"role":     string(user.Role),
			},
		}); err != nil {
			return fmt.Errorf("emit api_user.created: %w", err)
		}
		result = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetAPIUser retrieves an API user by ID (admin only)
func (s *AuthService) GetAPIUser(ctx context.Context, id uuid.UUID, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can get API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.factory.APIUserRepository().GetByID(ctx, id)
}

// GetAPIUserByUsername retrieves an API user by username (admin only)
func (s *AuthService) GetAPIUserByUsername(ctx context.Context, username string, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can get API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.factory.APIUserRepository().GetByUsername(ctx, username)
}

// ListAPIUsers lists all API users (admin only)
func (s *AuthService) ListAPIUsers(ctx context.Context, requestingUser *entities.APIUser) ([]*entities.APIUser, error) {
	// Only admins can list API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.factory.APIUserRepository().List(ctx, repositories.ListOptions{})
}

// ListAPIUsersPage returns one keyset-paginated page of API users. Admin-only.
// The repo's scope check returns no rows for any non-admin scope, but we also
// gate explicitly at the service layer for the clear 403.
func (s *AuthService) ListAPIUsersPage(ctx context.Context, scope authz.Scope, filter repositories.APIUserListFilter) ([]*entities.APIUser, string, error) {
	if !scope.IsAdmin() {
		return nil, "", fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.factory.APIUserRepository().ListPage(ctx, scope, filter)
}

// UpdatePasswordRequest contains data for updating a password
type UpdatePasswordRequest struct {
	Password string
}

// UpdateAPIUserPassword updates an API user's password (admin only)
func (s *AuthService) UpdateAPIUserPassword(ctx context.Context, id uuid.UUID, req UpdatePasswordRequest, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can update API user passwords
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can update API user passwords")
	}

	if req.Password == "" {
		return nil, fmt.Errorf("password is required")
	}

	var result *entities.APIUser
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user, err := tx.APIUserRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Hash new password
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		user.PasswordHash = string(passwordHash)
		user.UpdatedAt = clock.Now()

		if err := tx.APIUserRepository().Update(ctx, user); err != nil {
			return fmt.Errorf("failed to update user: %w", err)
		}

		var diff events.DiffBuilder
		diff.SetRedacted("password_hash", true)

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAPIUserPasswordChanged,
			ResourceType: "api_user",
			ResourceID:   user.ID.String(),
			Payload:      map[string]any{"username": user.Username},
			Diff:         diff.Finalize(),
		}); err != nil {
			return fmt.Errorf("emit api_user.password_changed: %w", err)
		}
		result = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateRoleRequest contains data for updating a user's role
type UpdateRoleRequest struct {
	Role       entities.APIUserRole
	OperatorID *uuid.UUID // Required for operator-admin role
	AccountID  *uuid.UUID // Required for account-admin role
}

// UpdateAPIUserPermissions updates an API user's role/permissions (admin only).
// The method is named to match the RPC procedure path for P1 emit-coverage lint.
func (s *AuthService) UpdateAPIUserPermissions(ctx context.Context, id uuid.UUID, req UpdateRoleRequest, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can update API user roles
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can update API user roles")
	}

	if !req.Role.IsValid() {
		return nil, fmt.Errorf("invalid role: %s", req.Role)
	}

	// Validate role-specific requirements
	switch req.Role {
	case entities.RoleOperatorAdmin:
		if req.OperatorID == nil {
			return nil, fmt.Errorf("operator_id is required for operator-admin role")
		}
		if req.AccountID != nil {
			return nil, fmt.Errorf("account_id must not be set for operator-admin role")
		}
	case entities.RoleAccountAdmin:
		if req.AccountID == nil {
			return nil, fmt.Errorf("account_id is required for account-admin role")
		}
		if req.OperatorID != nil {
			return nil, fmt.Errorf("operator_id must not be set for account-admin role")
		}
	case entities.RoleAdmin:
		if req.OperatorID != nil || req.AccountID != nil {
			return nil, fmt.Errorf("operator_id and account_id must not be set for admin role")
		}
	}

	var result *entities.APIUser
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user, err := tx.APIUserRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}

		beforeRole := user.Role

		user.Role = req.Role
		user.OperatorID = req.OperatorID
		user.AccountID = req.AccountID
		user.UpdatedAt = clock.Now()

		if err := tx.APIUserRepository().Update(ctx, user); err != nil {
			return fmt.Errorf("failed to update user: %w", err)
		}

		var diff events.DiffBuilder
		diff.Set("role", string(beforeRole), string(user.Role))

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAPIUserPermissionsChanged,
			ResourceType: "api_user",
			ResourceID:   user.ID.String(),
			Payload:      map[string]any{"username": user.Username, "role": string(user.Role)},
			Diff:         diff.Finalize(),
		}); err != nil {
			return fmt.Errorf("emit api_user.permissions_changed: %w", err)
		}
		result = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteAPIUser deletes an API user (admin only)
func (s *AuthService) DeleteAPIUser(ctx context.Context, id uuid.UUID, requestingUser *entities.APIUser) error {
	// Only admins can delete API users
	if requestingUser.Role != entities.RoleAdmin {
		return fmt.Errorf("permission denied: only admins can delete API users")
	}
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user, err := tx.APIUserRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}
		if err := tx.APIUserRepository().Delete(ctx, id); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAPIUserDeleted,
			ResourceType: "api_user",
			ResourceID:   user.ID.String(),
			Payload:      map[string]any{"username": user.Username, "role": string(user.Role)},
		})
	})
}

// generateToken generates a JWT token for a user
func (s *AuthService) generateToken(user *entities.APIUser) (string, error) {
	now := clock.Now()
	claims := AuthClaims{
		UserID:   user.ID.String(),
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "nis",
			Subject:   user.ID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}
