package handlers

import (
	"encoding/base64"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlmodels "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// GetSSOConfig must surface the OIDC callback_url even when an organization has
// no SSO config yet: operators need that redirect URI to register with their
// IdP *before* they can save a config. Regression — the handler used to return
// not_found in that case, which the UI rendered as "server.public_url is not
// configured on this server", hiding the redirect URI entirely.
func TestGetSSOConfig_ReturnsCallbackURLWhenNoConfigExists(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&sqlmodels.OrganizationSSOConfigModel{}))

	factory := persistence.NewSQLRepositoryFactoryFromDB(db)
	key := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	enc, err := encryption.NewChaChaEncryptor(map[string]string{"default": key}, "default")
	require.NoError(t, err)

	orgSvc := services.NewOrganizationService(factory, enc)
	permSvc := services.NewPermissionService(
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.OrganizationRepository(),
	)
	handler := NewOrganizationHandler(orgSvc, permSvc, "https://nis.example.com")

	// Admin role short-circuits the SSO permission check without touching the DB.
	ctx := ctxWithUser(&entities.APIUser{ID: uuid.New(), Role: entities.RoleAdmin})

	resp, err := handler.GetSSOConfig(ctx, connect.NewRequest(&pb.GetSSOConfigRequest{
		OrganizationId: uuid.New().String(),
	}))
	require.NoError(t, err)
	require.Equal(t, "https://nis.example.com/auth/oidc/callback", resp.Msg.GetCallbackUrl())
	require.Nil(t, resp.Msg.GetConfig(), "no SSO config should be reported when none exists")
}

// With server.public_url unset, callback_url is empty (nothing to register yet)
// but the call must still succeed rather than erroring.
func TestGetSSOConfig_EmptyCallbackWhenNoPublicURL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&sqlmodels.OrganizationSSOConfigModel{}))

	factory := persistence.NewSQLRepositoryFactoryFromDB(db)
	key := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	enc, err := encryption.NewChaChaEncryptor(map[string]string{"default": key}, "default")
	require.NoError(t, err)

	orgSvc := services.NewOrganizationService(factory, enc)
	permSvc := services.NewPermissionService(
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.OrganizationRepository(),
	)
	handler := NewOrganizationHandler(orgSvc, permSvc, "")

	ctx := ctxWithUser(&entities.APIUser{ID: uuid.New(), Role: entities.RoleAdmin})

	resp, err := handler.GetSSOConfig(ctx, connect.NewRequest(&pb.GetSSOConfigRequest{
		OrganizationId: uuid.New().String(),
	}))
	require.NoError(t, err)
	require.Empty(t, resp.Msg.GetCallbackUrl())
}
