package services

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// AuthService handles authentication and authorization
type AuthService struct {
	apiUserRepo repositories.APIUserRepository
	jwtSecret   []byte
	tokenTTL    time.Duration
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
	apiUserRepo repositories.APIUserRepository,
	jwtSecret string,
	tokenTTL time.Duration,
) *AuthService {
	if tokenTTL == 0 {
		tokenTTL = 24 * time.Hour // Default to 24 hours
	}
	return &AuthService{
		apiUserRepo: apiUserRepo,
		jwtSecret:   []byte(jwtSecret),
		tokenTTL:    tokenTTL,
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
	user, err := s.apiUserRepo.GetByUsername(ctx, req.Username)
	if err != nil {
		if err == repositories.ErrNotFound {
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

	user, err := s.apiUserRepo.GetByID(ctx, userID)
	if err != nil {
		if err == repositories.ErrNotFound {
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
	existing, err := s.apiUserRepo.GetByUsername(ctx, req.Username)
	if err == nil && existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// Hash password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &entities.APIUser{
		ID:           uuid.New(),
		Username:     req.Username,
		PasswordHash: string(passwordHash),
		Role:         req.Role,
		OperatorID:   req.OperatorID,
		AccountID:    req.AccountID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	err = s.apiUserRepo.Create(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return user, nil
}

// GetAPIUser retrieves an API user by ID (admin only)
func (s *AuthService) GetAPIUser(ctx context.Context, id uuid.UUID, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can get API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.apiUserRepo.GetByID(ctx, id)
}

// GetAPIUserByUsername retrieves an API user by username (admin only)
func (s *AuthService) GetAPIUserByUsername(ctx context.Context, username string, requestingUser *entities.APIUser) (*entities.APIUser, error) {
	// Only admins can get API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.apiUserRepo.GetByUsername(ctx, username)
}

// ListAPIUsers lists all API users (admin only)
func (s *AuthService) ListAPIUsers(ctx context.Context, requestingUser *entities.APIUser) ([]*entities.APIUser, error) {
	// Only admins can list API users
	if requestingUser.Role != entities.RoleAdmin {
		return nil, fmt.Errorf("permission denied: only admins can view API users")
	}
	return s.apiUserRepo.List(ctx, repositories.ListOptions{})
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

	user, err := s.apiUserRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Hash new password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user.PasswordHash = string(passwordHash)
	user.UpdatedAt = time.Now()

	err = s.apiUserRepo.Update(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return user, nil
}

// UpdateRoleRequest contains data for updating a user's role
type UpdateRoleRequest struct {
	Role       entities.APIUserRole
	OperatorID *uuid.UUID // Required for operator-admin role
	AccountID  *uuid.UUID // Required for account-admin role
}

// UpdateAPIUserRole updates an API user's role (admin only)
func (s *AuthService) UpdateAPIUserRole(ctx context.Context, id uuid.UUID, req UpdateRoleRequest, requestingUser *entities.APIUser) (*entities.APIUser, error) {
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

	user, err := s.apiUserRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	user.Role = req.Role
	user.OperatorID = req.OperatorID
	user.AccountID = req.AccountID
	user.UpdatedAt = time.Now()

	err = s.apiUserRepo.Update(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return user, nil
}

// DeleteAPIUser deletes an API user (admin only)
func (s *AuthService) DeleteAPIUser(ctx context.Context, id uuid.UUID, requestingUser *entities.APIUser) error {
	// Only admins can delete API users
	if requestingUser.Role != entities.RoleAdmin {
		return fmt.Errorf("permission denied: only admins can delete API users")
	}
	return s.apiUserRepo.Delete(ctx, id)
}

// generateToken generates a JWT token for a user
func (s *AuthService) generateToken(user *entities.APIUser) (string, error) {
	now := time.Now()
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
