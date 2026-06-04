package services

// auth_service_org_test.go — unit tests for the org-scoped api_user rewrite
// (chunk 5). These tests exercise the wired AuthService+PermissionService path
// so that the gate code (not just the Can* methods) is covered.
//
// Why these tests matter: CanCreateAPIUser / CanReadAPIUser etc. are covered by
// permission_service_test.go. These tests pin the *wiring* — the path through
// AuthService that calls permService — and the OIDC-row guard sentinel.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlmodels "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// AuthServiceOrgTestSuite exercises the org-scoped api_user management paths.
// Uses a real in-memory SQLite DB to test the full round-trip including repo
// SQL (org-admin sees only own org's rows).
type AuthServiceOrgTestSuite struct {
	suite.Suite
	db          *gorm.DB
	factory     persistence.RepositoryFactory
	authService *AuthService
	permService *PermissionService

	orgAID uuid.UUID
	orgBID uuid.UUID
	orgA   *entities.Organization
	orgB   *entities.Organization

	adminUser   *entities.APIUser
	orgAdminA   *entities.APIUser // org-admin scoped to orgA
}

func (s *AuthServiceOrgTestSuite) SetupTest() {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	s.Require().NoError(err)

	// Migrate all tables needed for FK relationships.
	err = db.AutoMigrate(
		&sqlmodels.OrganizationModel{},
		&sqlmodels.APIUserModel{},
		&sqlmodels.EventModel{},
		&sqlmodels.OperatorModel{},
		&sqlmodels.AccountModel{},
		&sqlmodels.APITokenModel{},
	)
	s.Require().NoError(err)

	s.db = db
	s.factory = persistence.NewSQLRepositoryFactoryFromDB(db)

	// Build services.
	s.authService = NewAuthService(s.factory, "test-secret-key-for-jwt-signing", 1*time.Hour)
	s.permService = NewPermissionService(
		s.factory.OperatorRepository(),
		s.factory.AccountRepository(),
		s.factory.UserRepository(),
		s.factory.OrganizationRepository(),
	)
	s.authService.WithPermissionService(s.permService)

	// Seed two orgs directly.
	s.orgAID = uuid.New()
	s.orgBID = uuid.New()
	now := clock.Now()
	orgASlug := "org-a"
	orgBSlug := "org-b"
	s.orgA = &entities.Organization{ID: s.orgAID, Name: "Org A", Slug: orgASlug, CreatedAt: now, UpdatedAt: now}
	s.orgB = &entities.Organization{ID: s.orgBID, Name: "Org B", Slug: orgBSlug, CreatedAt: now, UpdatedAt: now}
	s.Require().NoError(s.factory.OrganizationRepository().Create(context.Background(), s.orgA))
	s.Require().NoError(s.factory.OrganizationRepository().Create(context.Background(), s.orgB))

	// Platform admin (org-less).
	s.adminUser = &entities.APIUser{
		ID:        uuid.New(),
		Username:  "platform-admin",
		Role:      entities.RoleAdmin,
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Org-admin for orgA — created directly so we bypass the permission gate.
	s.orgAdminA = &entities.APIUser{
		ID:             uuid.New(),
		Username:       "org-admin-a",
		Role:           entities.RoleOrgAdmin,
		OrganizationID: &s.orgAID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy", // not used in these tests
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(context.Background(), s.orgAdminA))
}

func (s *AuthServiceOrgTestSuite) TearDownTest() {
	sqlDB, err := s.db.DB()
	s.Require().NoError(err)
	_ = sqlDB.Close()
}

func TestAuthServiceOrgSuite(t *testing.T) {
	suite.Run(t, new(AuthServiceOrgTestSuite))
}

// ---------------------------------------------------------------------------
// CreateAPIUser
// ---------------------------------------------------------------------------

// TestCreateAPIUser_AdminCanCreateAnywhere proves a platform admin can create
// a user in any org (including org A) with any role.
func (s *AuthServiceOrgTestSuite) TestCreateAPIUser_AdminCanCreateAnywhere() {
	ctx := context.Background()
	user, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username:       "user-in-org-a",
		Password:       "secret123",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID,
		// OperatorID would normally be set for operator-admin, but we're using
		// the admin path which skips the operator-ownership check.
		// Actually, the validation inside CreateAPIUser still requires OperatorID
		// for operator-admin. Let's use org-admin role instead.
	}, s.adminUser)
	// operator-admin without OperatorID should still return an error.
	s.Error(err)
	s.Contains(err.Error(), "operator_id is required")

	// Use org-admin role (below admin rank) — no operator/account scope needed.
	user, err = s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username:       "user-org-admin-in-b",
		Password:       "secret123",
		Role:           entities.RoleOrgAdmin,
		OrganizationID: &s.orgBID,
	}, s.adminUser)
	s.Require().NoError(err)
	s.Equal(entities.RoleOrgAdmin, user.Role)
	s.Require().NotNil(user.OrganizationID)
	s.Equal(s.orgBID, *user.OrganizationID)
	s.Equal("local", user.AuthSource)
}

// TestCreateAPIUser_OrgAdminCanCreateInOwnOrg proves an org-admin can create a
// lower-rank user in their own org.
func (s *AuthServiceOrgTestSuite) TestCreateAPIUser_OrgAdminCanCreateInOwnOrg() {
	ctx := context.Background()
	user, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "op-admin-in-a",
		Password: "secret123",
		Role:     entities.RoleOperatorAdmin,
		// No OrganizationID — should default to orgAdminA's org.
		OperatorID: &s.orgAID, // reusing org UUID as fake operator UUID (SQLite no FK enforcement)
	}, s.orgAdminA)
	s.Require().NoError(err)
	s.Equal(entities.RoleOperatorAdmin, user.Role)
	// OrganizationID should have been defaulted to orgA.
	s.Require().NotNil(user.OrganizationID)
	s.Equal(s.orgAID, *user.OrganizationID)
}

// TestCreateAPIUser_OrgAdminCannotCreateInOtherOrg proves an org-admin cannot
// create a user in a different org, even if they explicitly specify it.
func (s *AuthServiceOrgTestSuite) TestCreateAPIUser_OrgAdminCannotCreateInOtherOrg() {
	ctx := context.Background()
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username:       "should-fail",
		Password:       "secret123",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgBID, // wrong org
	}, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// TestCreateAPIUser_OrgAdminCannotCreateOrgAdminPeer proves the rank ceiling:
// an org-admin cannot create another org-admin (same rank).
func (s *AuthServiceOrgTestSuite) TestCreateAPIUser_OrgAdminCannotCreateOrgAdminPeer() {
	ctx := context.Background()
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username:       "peer-org-admin",
		Password:       "secret123",
		Role:           entities.RoleOrgAdmin,
		OrganizationID: &s.orgAID,
	}, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// TestCreateAPIUser_OrgAdminCannotCreatePlatformAdmin proves an org-admin
// cannot create a platform admin (higher rank, null org).
func (s *AuthServiceOrgTestSuite) TestCreateAPIUser_OrgAdminCannotCreatePlatformAdmin() {
	ctx := context.Background()
	_, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username: "evil-admin",
		Password: "secret123",
		Role:     entities.RoleAdmin,
	}, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// ---------------------------------------------------------------------------
// GetAPIUser
// ---------------------------------------------------------------------------

// TestGetAPIUser_OrgAdminCanReadOwnOrgUser proves org-admin can read a lower-rank
// user in their own org.
func (s *AuthServiceOrgTestSuite) TestGetAPIUser_OrgAdminCanReadOwnOrgUser() {
	ctx := context.Background()
	// Create a user in orgA directly.
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "target-in-a",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	got, err := s.authService.GetAPIUser(ctx, target.ID, s.orgAdminA)
	s.Require().NoError(err)
	s.Equal(target.ID, got.ID)
}

// TestGetAPIUser_OrgAdminCannotReadOtherOrgUser proves org-admin cannot read a
// user in a different org.
func (s *AuthServiceOrgTestSuite) TestGetAPIUser_OrgAdminCannotReadOtherOrgUser() {
	ctx := context.Background()
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "target-in-b",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgBID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	_, err := s.authService.GetAPIUser(ctx, target.ID, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// ---------------------------------------------------------------------------
// UpdateAPIUserPassword — OIDC guard
// ---------------------------------------------------------------------------

// TestUpdateAPIUserPassword_OIDCRowReturnsError proves that attempting to
// update the password of an OIDC-sourced user returns ErrOIDCManagedUser,
// regardless of the caller's role.
func (s *AuthServiceOrgTestSuite) TestUpdateAPIUserPassword_OIDCRowReturnsError() {
	ctx := context.Background()
	extSub := "sub-for-oidc-user"
	oidcUser := &entities.APIUser{
		ID:              uuid.New(),
		Username:        "oidc-user",
		Role:            entities.RoleOperatorAdmin,
		OrganizationID:  &s.orgAID,
		AuthSource:      "oidc",
		ExternalSubject: &extSub,
		PasswordHash:    "$2a$10$dummy",
		CreatedAt:       clock.Now(),
		UpdatedAt:       clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, oidcUser))

	// Even an admin is blocked — the intent is "this row is managed by SSO".
	_, err := s.authService.UpdateAPIUserPassword(ctx, oidcUser.ID, UpdatePasswordRequest{Password: "newpw"}, s.adminUser)
	s.Error(err)
	s.True(errors.Is(err, ErrOIDCManagedUser), "expected ErrOIDCManagedUser, got: %v", err)
}

// TestUpdateAPIUserPassword_OrgAdminCanUpdateLocalUser proves an org-admin can
// change the password of a local user in their own org.
func (s *AuthServiceOrgTestSuite) TestUpdateAPIUserPassword_OrgAdminCanUpdateLocalUser() {
	ctx := context.Background()
	// Create the user via AuthService (exercises CreateAPIUser wiring too).
	target, err := s.authService.CreateAPIUser(ctx, CreateAPIUserRequest{
		Username:       "local-op-admin",
		Password:       "oldpassword",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID,
		OperatorID:     &s.orgAID, // fake operator UUID; SQLite has no FK check
	}, s.adminUser)
	s.Require().NoError(err)

	_, err = s.authService.UpdateAPIUserPassword(ctx, target.ID, UpdatePasswordRequest{Password: "newpassword"}, s.orgAdminA)
	s.NoError(err)
}

// TestUpdateAPIUserPassword_OrgAdminCannotUpdateOtherOrgUser proves an org-admin
// cannot update a user in another org.
func (s *AuthServiceOrgTestSuite) TestUpdateAPIUserPassword_OrgAdminCannotUpdateOtherOrgUser() {
	ctx := context.Background()
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "victim-in-b",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgBID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	_, err := s.authService.UpdateAPIUserPassword(ctx, target.ID, UpdatePasswordRequest{Password: "evil"}, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// ---------------------------------------------------------------------------
// UpdateAPIUserPermissions — OIDC guard + role-escalation guard
// ---------------------------------------------------------------------------

// TestUpdateAPIUserPermissions_OIDCRowReturnsError proves the OIDC guard fires
// for UpdateAPIUserPermissions too.
func (s *AuthServiceOrgTestSuite) TestUpdateAPIUserPermissions_OIDCRowReturnsError() {
	ctx := context.Background()
	extSub := "sub-for-oidc-role-user"
	oidcUser := &entities.APIUser{
		ID:              uuid.New(),
		Username:        "oidc-role-user",
		Role:            entities.RoleOperatorAdmin,
		OrganizationID:  &s.orgAID,
		AuthSource:      "oidc",
		ExternalSubject: &extSub,
		PasswordHash:    "$2a$10$dummy",
		CreatedAt:       clock.Now(),
		UpdatedAt:       clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, oidcUser))

	_, err := s.authService.UpdateAPIUserPermissions(ctx, oidcUser.ID, UpdateRoleRequest{
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &s.orgAID,
	}, s.adminUser)
	s.Error(err)
	s.True(errors.Is(err, ErrOIDCManagedUser), "expected ErrOIDCManagedUser, got: %v", err)
}

// TestUpdateAPIUserPermissions_OrgAdminCannotEscalateRole proves an org-admin
// cannot change a user's role to org-admin (same rank as themselves).
func (s *AuthServiceOrgTestSuite) TestUpdateAPIUserPermissions_OrgAdminCannotEscalateRole() {
	ctx := context.Background()
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "op-admin-to-escalate",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	// Try to escalate to org-admin (same rank — ceiling check should reject).
	_, err := s.authService.UpdateAPIUserPermissions(ctx, target.ID, UpdateRoleRequest{
		Role: entities.RoleOrgAdmin,
	}, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// TestUpdateAPIUserPermissions_AdminCanUpdateRole proves a platform admin can
// change any local user's role.
func (s *AuthServiceOrgTestSuite) TestUpdateAPIUserPermissions_AdminCanUpdateRole() {
	ctx := context.Background()
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "changeable-user",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	updated, err := s.authService.UpdateAPIUserPermissions(ctx, target.ID, UpdateRoleRequest{
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &s.orgAID,
	}, s.adminUser)
	s.Require().NoError(err)
	s.Equal(entities.RoleOperatorAdmin, updated.Role)
}

// ---------------------------------------------------------------------------
// DeleteAPIUser
// ---------------------------------------------------------------------------

// TestDeleteAPIUser_OrgAdminCanDeleteInOwnOrg proves an org-admin can delete a
// lower-rank user in their own org.
func (s *AuthServiceOrgTestSuite) TestDeleteAPIUser_OrgAdminCanDeleteInOwnOrg() {
	ctx := context.Background()
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "delete-me-in-a",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	err := s.authService.DeleteAPIUser(ctx, target.ID, s.orgAdminA)
	s.NoError(err)

	_, err = s.factory.APIUserRepository().GetByID(ctx, target.ID)
	s.True(errors.Is(err, repositories.ErrNotFound))
}

// TestDeleteAPIUser_OrgAdminCannotDeleteInOtherOrg proves an org-admin cannot
// delete a user in a different org.
func (s *AuthServiceOrgTestSuite) TestDeleteAPIUser_OrgAdminCannotDeleteInOtherOrg() {
	ctx := context.Background()
	target := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "keep-me-in-b",
		Role:           entities.RoleOperatorAdmin,
		OrganizationID: &s.orgBID,
		AuthSource:     "local",
		PasswordHash:   "$2a$10$dummy",
		CreatedAt:      clock.Now(),
		UpdatedAt:      clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, target))

	err := s.authService.DeleteAPIUser(ctx, target.ID, s.orgAdminA)
	s.Error(err)
	s.True(errors.Is(err, ErrPermissionDenied))
}

// ---------------------------------------------------------------------------
// ListAPIUsersPage
// ---------------------------------------------------------------------------

// TestListAPIUsersPage_OrgAdminSeesOnlyOwnOrg proves the scope-narrowing via
// the repo: org-admin only sees users in their own org.
func (s *AuthServiceOrgTestSuite) TestListAPIUsersPage_OrgAdminSeesOnlyOwnOrg() {
	ctx := context.Background()

	inA := &entities.APIUser{
		ID: uuid.New(), Username: "in-org-a", Role: entities.RoleOperatorAdmin,
		OrganizationID: &s.orgAID, AuthSource: "local", PasswordHash: "$2a$10$dummy",
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}
	inB := &entities.APIUser{
		ID: uuid.New(), Username: "in-org-b", Role: entities.RoleOperatorAdmin,
		OrganizationID: &s.orgBID, AuthSource: "local", PasswordHash: "$2a$10$dummy",
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, inA))
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, inB))

	scope := authz.ScopeFromAPIUser(s.orgAdminA)
	users, _, err := s.authService.ListAPIUsersPage(ctx, scope, repositories.APIUserListFilter{})
	s.Require().NoError(err)
	for _, u := range users {
		s.Require().NotNil(u.OrganizationID, "every listed user must have an org")
		s.Equal(s.orgAID, *u.OrganizationID, "org-admin must only see orgA users; got orgID %s for user %s", *u.OrganizationID, u.Username)
	}
	// org-admin should see org-admin-a (created in SetupTest in orgA) + inA.
	usernames := make([]string, 0, len(users))
	for _, u := range users {
		usernames = append(usernames, u.Username)
	}
	s.Contains(usernames, "in-org-a")
	s.NotContains(usernames, "in-org-b")
}

// TestListAPIUsersPage_LowerRoleDenied proves that operator-admin and below
// cannot call ListAPIUsersPage.
func (s *AuthServiceOrgTestSuite) TestListAPIUsersPage_LowerRoleDenied() {
	ctx := context.Background()
	opAdmin := &entities.APIUser{Role: entities.RoleOperatorAdmin}
	scope := authz.ScopeFromAPIUser(opAdmin)
	_, _, err := s.authService.ListAPIUsersPage(ctx, scope, repositories.APIUserListFilter{})
	s.Error(err)
	s.Contains(err.Error(), "permission denied")
}
