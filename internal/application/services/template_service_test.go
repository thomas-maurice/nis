package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gorm.io/gorm"

	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// TemplateServiceTestSuite exercises the P6 template service end-to-end
// inside an in-memory SQLite DB. It mirrors ScopedSigningKeyServiceTestSuite's
// setup so the two share the same migration + factory wiring. No external
// services or NATS involvement — that lives in tests/e2e/templates_test.go.
type TemplateServiceTestSuite struct {
	suite.Suite
	ctx              context.Context
	db               *gorm.DB
	encryptor        encryption.Encryptor
	factory          persistence.RepositoryFactory
	jwtService       *JWTService
	accountService   *AccountService
	operatorService  *OperatorService
	scopedKeyService *ScopedSigningKeyService
	templateService  *TemplateService
	permService      *PermissionService
}

func (s *TemplateServiceTestSuite) SetupSuite() {
	s.ctx = context.Background()
	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	sqlDB, err := db.DB()
	require.NoError(s.T(), err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(s.T(), goose.SetDialect("sqlite3"))
	require.NoError(s.T(), goose.Up(sqlDB, "sqlite"))

	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(s.T(), err)
	s.encryptor = enc

	s.jwtService = NewJWTService(s.encryptor)
	s.factory = persistence.NewSQLRepositoryFactoryFromDB(s.db)

	s.accountService = NewAccountService(s.factory, s.jwtService, s.encryptor)
	s.operatorService = NewOperatorService(s.factory, s.accountService, s.jwtService, s.encryptor)
	s.scopedKeyService = NewScopedSigningKeyService(s.factory, s.jwtService, s.encryptor)
	s.permService = NewPermissionService(
		s.factory.OperatorRepository(),
		s.factory.AccountRepository(),
		s.factory.UserRepository(),
	)
	s.templateService = NewTemplateService(s.factory, s.permService)
}

func (s *TemplateServiceTestSuite) TearDownSuite() {
	_ = sql.Close(s.db)
}

func (s *TemplateServiceTestSuite) TearDownTest() {
	s.db.Exec("DELETE FROM template_versions")
	s.db.Exec("DELETE FROM templates")
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")
}

func TestTemplateServiceSuite(t *testing.T) {
	suite.Run(t, new(TemplateServiceTestSuite))
}

// makeOperator returns the operator UUID for tests that don't care about
// the full operator entity.
func (s *TemplateServiceTestSuite) makeOperator(name string) uuid.UUID {
	op, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{Name: name})
	require.NoError(s.T(), err)
	return op.ID
}

func (s *TemplateServiceTestSuite) TestCreateTemplate_StampsV1() {
	opID := s.makeOperator("op-create-v1")

	tpl, ver, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID:  opID,
		Name:        "reader",
		Description: "Read-only",
		SubAllow:    []string{"events.>"},
		ChangeNote:  "initial",
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, tpl.LatestVersion)
	assert.Equal(s.T(), 1, ver.VersionNumber)
	assert.Equal(s.T(), []string{"events.>"}, ver.SubAllow)
	assert.Empty(s.T(), ver.PubAllow)
	assert.Equal(s.T(), "initial", ver.ChangeNote)
}

func (s *TemplateServiceTestSuite) TestCreateTemplate_RejectsReservedNames() {
	opID := s.makeOperator("op-reserved")
	for _, name := range []string{"default", "system"} {
		_, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
			OperatorID: opID,
			Name:       name,
		})
		require.Error(s.T(), err, "expected %q to be refused", name)
		assert.True(s.T(), errors.Is(err, ErrTemplateNameReserved), "expected ErrTemplateNameReserved for %q, got %v", name, err)
	}
}

func (s *TemplateServiceTestSuite) TestCreateTemplate_RejectsDuplicateName() {
	opID := s.makeOperator("op-dup")
	_, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "dup",
	})
	require.NoError(s.T(), err)
	_, _, err = s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "dup",
	})
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, repositories.ErrAlreadyExists))
}

func (s *TemplateServiceTestSuite) TestUpdateTemplate_DescriptionOnly_DoesNotBump() {
	opID := s.makeOperator("op-desc-only")
	tpl, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID:  opID,
		Name:        "t",
		Description: "old",
		PubAllow:    []string{">"},
	})
	require.NoError(s.T(), err)

	newDesc := "new"
	updated, ver, err := s.templateService.UpdateTemplate(s.ctx, tpl.ID, UpdateTemplateRequest{
		Description: &newDesc,
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "new", updated.Description)
	assert.Equal(s.T(), 1, updated.LatestVersion, "description-only edit must not bump")
	assert.Nil(s.T(), ver, "no new version row on description-only edit")
}

func (s *TemplateServiceTestSuite) TestUpdateTemplate_PermissionChange_BumpsVersion() {
	opID := s.makeOperator("op-bump")
	tpl, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "t",
		SubAllow:   []string{"events.>"},
	})
	require.NoError(s.T(), err)

	updated, newVer, err := s.templateService.UpdateTemplate(s.ctx, tpl.ID, UpdateTemplateRequest{
		PermissionsProvided: true,
		SubAllow:            []string{"events.>", "metrics.>"},
		ChangeNote:          "add metrics",
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 2, updated.LatestVersion)
	require.NotNil(s.T(), newVer)
	assert.Equal(s.T(), 2, newVer.VersionNumber)
	assert.Equal(s.T(), "add metrics", newVer.ChangeNote)
	assert.Equal(s.T(), []string{"events.>", "metrics.>"}, newVer.SubAllow)
}

func (s *TemplateServiceTestSuite) TestUpdateTemplate_NoDiff_NoBump() {
	opID := s.makeOperator("op-nodiff")
	tpl, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "t",
		SubAllow:   []string{"events.>"},
	})
	require.NoError(s.T(), err)

	updated, newVer, err := s.templateService.UpdateTemplate(s.ctx, tpl.ID, UpdateTemplateRequest{
		PermissionsProvided: true,
		SubAllow:            []string{"events.>"},
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, updated.LatestVersion, "identical permissions must not bump")
	assert.Nil(s.T(), newVer)
}

func (s *TemplateServiceTestSuite) TestDeleteTemplate_BlockedByDependents() {
	opID := s.makeOperator("op-del-block")
	tpl, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "t",
		PubAllow:   []string{">"},
	})
	require.NoError(s.T(), err)

	// Create an account + SSK pinned to the template.
	acc, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: opID, Name: "acc"})
	require.NoError(s.T(), err)
	ssk, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: acc.ID,
		Name:      "templated",
		TemplateRef: &TemplateRef{
			OperatorID:   opID,
			TemplateName: "t",
		},
	})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ssk.TemplateID)

	err = s.templateService.DeleteTemplate(s.ctx, tpl.ID)
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, ErrTemplateHasDependents), "expected ErrTemplateHasDependents, got %v", err)

	// Detach the SSK, then delete succeeds.
	_, err = s.scopedKeyService.DetachScopedKeyTemplate(s.ctx, ssk.ID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.templateService.DeleteTemplate(s.ctx, tpl.ID))
}

func (s *TemplateServiceTestSuite) TestBumpScopedKey_AppliesNewVersion() {
	opID := s.makeOperator("op-bump-ssk")
	acc, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: opID, Name: "acc"})
	require.NoError(s.T(), err)

	tpl, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "t",
		PubAllow:   []string{"v1.>"},
	})
	require.NoError(s.T(), err)
	ssk, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID:   acc.ID,
		Name:        "k",
		TemplateRef: &TemplateRef{OperatorID: opID, TemplateName: "t"},
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"v1.>"}, ssk.PubAllow)

	// Bump template to v2.
	_, _, err = s.templateService.UpdateTemplate(s.ctx, tpl.ID, UpdateTemplateRequest{
		PermissionsProvided: true,
		PubAllow:            []string{"v2.>"},
	})
	require.NoError(s.T(), err)

	// SSK still on v1 until explicit bump.
	stillOld, err := s.scopedKeyService.GetScopedSigningKey(s.ctx, ssk.ID)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), stillOld.TemplateVersion)
	assert.Equal(s.T(), 1, *stillOld.TemplateVersion, "templates must not auto-cascade")
	assert.Equal(s.T(), []string{"v1.>"}, stillOld.PubAllow)

	// Bump applies v2 snapshot.
	bumped, err := s.scopedKeyService.BumpScopedKeyTemplate(s.ctx, ssk.ID, 0)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), bumped.TemplateVersion)
	assert.Equal(s.T(), 2, *bumped.TemplateVersion)
	assert.Equal(s.T(), []string{"v2.>"}, bumped.PubAllow)
	assert.False(s.T(), bumped.TemplateDrifted, "bump clears drift")
}

func (s *TemplateServiceTestSuite) TestDirectEdit_FlagsDrifted() {
	opID := s.makeOperator("op-drift")
	acc, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: opID, Name: "acc"})
	require.NoError(s.T(), err)

	_, _, err = s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "t",
		PubAllow:   []string{">"},
	})
	require.NoError(s.T(), err)
	ssk, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID:   acc.ID,
		Name:        "k",
		TemplateRef: &TemplateRef{OperatorID: opID, TemplateName: "t"},
	})
	require.NoError(s.T(), err)
	assert.False(s.T(), ssk.TemplateDrifted)

	// Direct permission edit on a templated SSK ⇒ drifted=true.
	edited, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, ssk.ID, UpdateScopedSigningKeyRequest{
		PubAllow: []string{">", "custom.>"},
	})
	require.NoError(s.T(), err)
	assert.True(s.T(), edited.TemplateDrifted, "drift flag must flip on direct permission edit")
	require.NotNil(s.T(), edited.TemplateID, "edit must NOT auto-detach the template ref")
}

func (s *TemplateServiceTestSuite) TestCreateScopedKey_CrossOperatorTemplateRejected() {
	opA := s.makeOperator("op-iso-a")
	opB := s.makeOperator("op-iso-b")

	// Template lives under B.
	_, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opB,
		Name:       "b-template",
		PubAllow:   []string{">"},
	})
	require.NoError(s.T(), err)

	// Account lives under A — using B's template should fail with the
	// foreign-operator sentinel.
	accA, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: opA, Name: "a-acc"})
	require.NoError(s.T(), err)

	_, err = s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accA.ID,
		Name:      "a-ssk",
		TemplateRef: &TemplateRef{
			OperatorID:   opB,
			TemplateName: "b-template",
		},
	})
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, ErrTemplateRefForeignOperator), "expected ErrTemplateRefForeignOperator, got %v", err)
}

func (s *TemplateServiceTestSuite) TestListTemplateVersions_ReturnsHistoryDesc() {
	opID := s.makeOperator("op-history")
	tpl, _, err := s.templateService.CreateTemplate(s.ctx, CreateTemplateRequest{
		OperatorID: opID,
		Name:       "t",
		PubAllow:   []string{"a.>"},
	})
	require.NoError(s.T(), err)
	for i, perm := range []string{"b.>", "c.>"} {
		_, _, err := s.templateService.UpdateTemplate(s.ctx, tpl.ID, UpdateTemplateRequest{
			PermissionsProvided: true,
			PubAllow:            []string{perm},
		})
		require.NoError(s.T(), err, "bump %d", i)
	}

	versions, err := s.templateService.ListTemplateVersions(s.ctx, tpl.ID)
	require.NoError(s.T(), err)
	require.Len(s.T(), versions, 3)
	// Newest first.
	assert.Equal(s.T(), 3, versions[0].VersionNumber)
	assert.Equal(s.T(), 1, versions[2].VersionNumber)
}
