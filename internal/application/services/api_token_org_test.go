package services

// api_token_org_test.go — unit tests for the org-scoped APITokenService paths
// (chunk 5). Tests org derivation for operator-admin/account-admin tokens,
// mandatory org for org-admin tokens, and SyntheticAPIUser org carry-through.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlmodels "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type APITokenOrgTestSuite struct {
	suite.Suite
	db           *gorm.DB
	factory      persistence.RepositoryFactory
	tokenService *APITokenService

	orgID    uuid.UUID
	opID     uuid.UUID
	acctID   uuid.UUID
	adminUID uuid.UUID
}

func (s *APITokenOrgTestSuite) SetupTest() {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	s.Require().NoError(err)

	err = db.AutoMigrate(
		&sqlmodels.OrganizationModel{},
		&sqlmodels.OperatorModel{},
		&sqlmodels.AccountModel{},
		&sqlmodels.APIUserModel{},
		&sqlmodels.APITokenModel{},
		&sqlmodels.EventModel{},
		&sqlmodels.WebhookSubscriptionModel{},
		&sqlmodels.WebhookDeliveryModel{},
	)
	s.Require().NoError(err)

	s.db = db
	s.factory = persistence.NewSQLRepositoryFactoryFromDB(db)
	s.tokenService = NewAPITokenService(s.factory)

	now := clock.Now()
	ctx := context.Background()

	// Seed org.
	s.orgID = uuid.New()
	slug := "test-org"
	org := &entities.Organization{ID: s.orgID, Name: "Test Org", Slug: slug, CreatedAt: now, UpdatedAt: now}
	s.Require().NoError(s.factory.OrganizationRepository().Create(ctx, org))

	// Seed operator in that org.
	s.opID = uuid.New()
	op := &entities.Operator{
		ID:             s.opID,
		Name:           "test-op",
		OrganizationID: s.orgID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.Require().NoError(s.factory.OperatorRepository().Create(ctx, op))

	// Seed account under that operator.
	s.acctID = uuid.New()
	acct := &entities.Account{
		ID:         s.acctID,
		OperatorID: s.opID,
		Name:       "test-acct",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.Require().NoError(s.factory.AccountRepository().Create(ctx, acct))

	// Seed admin api_user (creator).
	s.adminUID = uuid.New()
	admin := &entities.APIUser{
		ID:           s.adminUID,
		Username:     "admin",
		Role:         entities.RoleAdmin,
		AuthSource:   "local",
		PasswordHash: "$2a$10$dummy",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.Require().NoError(s.factory.APIUserRepository().Create(ctx, admin))
}

func (s *APITokenOrgTestSuite) TearDownTest() {
	sqlDB, err := s.db.DB()
	s.Require().NoError(err)
	_ = sqlDB.Close()
}

func TestAPITokenOrgSuite(t *testing.T) {
	suite.Run(t, new(APITokenOrgTestSuite))
}

// TestCreateToken_OperatorAdminDerivesOrg proves that when an operator-admin
// token is created without an explicit OrganizationID, CreateToken derives the
// org from the operator's OrganizationID.
func (s *APITokenOrgTestSuite) TestCreateToken_OperatorAdminDerivesOrg() {
	ctx := context.Background()
	token, _, err := s.tokenService.CreateToken(ctx, CreateAPITokenRequest{
		Name:            "op-admin-token",
		Role:            entities.RoleOperatorAdmin,
		OperatorID:      &s.opID,
		CreatedByUserID: &s.adminUID,
		// OrganizationID intentionally nil — must be derived.
	})
	s.Require().NoError(err)
	s.Require().NotNil(token.OrganizationID, "operator-admin token must have a derived OrganizationID")
	s.Equal(s.orgID, *token.OrganizationID)
}

// TestCreateToken_AccountAdminDerivesOrg proves that account-admin tokens
// derive org through the account → operator chain.
func (s *APITokenOrgTestSuite) TestCreateToken_AccountAdminDerivesOrg() {
	ctx := context.Background()
	token, _, err := s.tokenService.CreateToken(ctx, CreateAPITokenRequest{
		Name:            "acct-admin-token",
		Role:            entities.RoleAccountAdmin,
		AccountID:       &s.acctID,
		CreatedByUserID: &s.adminUID,
	})
	s.Require().NoError(err)
	s.Require().NotNil(token.OrganizationID, "account-admin token must have a derived OrganizationID")
	s.Equal(s.orgID, *token.OrganizationID)
}

// TestCreateToken_AdminTokenHasNilOrg proves platform-admin tokens keep
// OrganizationID == nil (they are platform-wide, not scoped to an org).
func (s *APITokenOrgTestSuite) TestCreateToken_AdminTokenHasNilOrg() {
	ctx := context.Background()
	token, _, err := s.tokenService.CreateToken(ctx, CreateAPITokenRequest{
		Name:            "admin-platform-token",
		Role:            entities.RoleAdmin,
		CreatedByUserID: &s.adminUID,
		// OrganizationID intentionally nil — admin tokens stay org-less.
	})
	s.Require().NoError(err)
	s.Nil(token.OrganizationID, "platform-admin token must not have an OrganizationID")
}

// TestCreateToken_OrgAdminTokenRequiresOrg proves that creating an org-admin
// token without providing an OrganizationID returns an error.
func (s *APITokenOrgTestSuite) TestCreateToken_OrgAdminTokenRequiresOrg() {
	ctx := context.Background()
	_, _, err := s.tokenService.CreateToken(ctx, CreateAPITokenRequest{
		Name:            "missing-org-admin-token",
		Role:            entities.RoleOrgAdmin,
		CreatedByUserID: &s.adminUID,
		// OrganizationID missing — should error.
	})
	s.Error(err)
	s.Contains(err.Error(), "organization_id is required for org-admin role tokens")
}

// TestCreateToken_OrgAdminTokenWithOrg proves an org-admin token is created
// successfully when OrganizationID is provided.
func (s *APITokenOrgTestSuite) TestCreateToken_OrgAdminTokenWithOrg() {
	ctx := context.Background()
	token, _, err := s.tokenService.CreateToken(ctx, CreateAPITokenRequest{
		Name:            "org-admin-token",
		Role:            entities.RoleOrgAdmin,
		OrganizationID:  &s.orgID,
		CreatedByUserID: &s.adminUID,
	})
	s.Require().NoError(err)
	s.Require().NotNil(token.OrganizationID)
	s.Equal(s.orgID, *token.OrganizationID)
}

// TestSyntheticAPIUser_CarriesOrganizationID proves that SyntheticAPIUser
// carries the OrganizationID from the token so that ScopeFromAPIUser returns
// the correct org-scoped scope for org-admin-role tokens (which the middleware
// uses to gate every authenticated request).
func (s *APITokenOrgTestSuite) TestSyntheticAPIUser_CarriesOrganizationID() {
	orgID := uuid.New()
	now := time.Now()
	token := &entities.APIToken{
		ID:             uuid.New(),
		Role:           entities.RoleOrgAdmin,
		OrganizationID: &orgID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	synthetic := s.tokenService.SyntheticAPIUser(token)
	s.Require().NotNil(synthetic.OrganizationID, "synthetic user must carry the token's OrganizationID")
	s.Equal(orgID, *synthetic.OrganizationID)
	s.Equal(entities.RoleOrgAdmin, synthetic.Role)
}

// TestSyntheticAPIUser_AdminTokenHasNilOrg proves that a platform-admin token's
// synthetic user has nil OrganizationID (preserving the "org-less admin" invariant).
func (s *APITokenOrgTestSuite) TestSyntheticAPIUser_AdminTokenHasNilOrg() {
	now := time.Now()
	token := &entities.APIToken{
		ID:        uuid.New(),
		Role:      entities.RoleAdmin,
		CreatedAt: now,
		UpdatedAt: now,
	}

	synthetic := s.tokenService.SyntheticAPIUser(token)
	s.Nil(synthetic.OrganizationID, "admin token synthetic user must have nil OrganizationID")
}
