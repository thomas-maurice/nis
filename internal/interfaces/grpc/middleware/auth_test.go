package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		want       string
	}{
		{
			name:       "valid bearer token",
			authHeader: "Bearer my-token-123",
			want:       "my-token-123",
		},
		{
			name:       "valid bearer token with lowercase",
			authHeader: "bearer my-token-123",
			want:       "my-token-123",
		},
		{
			name:       "valid bearer token with mixed case",
			authHeader: "BEARER my-token-123",
			want:       "my-token-123",
		},
		{
			name:       "empty header",
			authHeader: "",
			want:       "",
		},
		{
			name:       "missing token value",
			authHeader: "Bearer",
			want:       "",
		},
		{
			name:       "wrong scheme",
			authHeader: "Basic dXNlcjpwYXNz",
			want:       "",
		},
		{
			name:       "no scheme just token",
			authHeader: "my-token-123",
			want:       "",
		},
		{
			name:       "token with spaces in value",
			authHeader: "Bearer token with spaces",
			want:       "token with spaces",
		},
		{
			name:       "bearer with extra whitespace prefix",
			authHeader: " Bearer my-token",
			want:       "",
		},
		{
			name:       "jwt-like token",
			authHeader: "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123",
			want:       "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToken(tt.authHeader)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractAction(t *testing.T) {
	tests := []struct {
		name   string
		method string
		want   string
	}{
		// Create actions
		{name: "CreateOperator", method: "CreateOperator", want: "create"},
		{name: "CreateAccount", method: "CreateAccount", want: "create"},
		{name: "CreateUser", method: "CreateUser", want: "create"},

		// Update actions
		{name: "UpdateOperator", method: "UpdateOperator", want: "update"},
		{name: "UpdateAccount", method: "UpdateAccount", want: "update"},

		// Delete actions
		{name: "DeleteOperator", method: "DeleteOperator", want: "delete"},
		{name: "DeleteAccount", method: "DeleteAccount", want: "delete"},

		// Read actions - Get prefix
		{name: "GetOperator", method: "GetOperator", want: "read"},
		{name: "GetAccount", method: "GetAccount", want: "read"},

		// Read actions - List prefix
		{name: "ListOperators", method: "ListOperators", want: "read"},
		{name: "ListAccounts", method: "ListAccounts", want: "read"},

		// Unknown method defaults to read
		{name: "unknown method SyncCluster", method: "SyncCluster", want: "read"},
		{name: "unknown method GenerateInclude", method: "GenerateInclude", want: "read"},
		{name: "empty method", method: "", want: "read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAction(tt.method)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractResourceAndAction(t *testing.T) {
	tests := []struct {
		name         string
		procedure    string
		wantResource string
		wantAction   string
	}{
		// Standard services
		{
			name:         "create operator",
			procedure:    "/nis.v1.OperatorService/CreateOperator",
			wantResource: "operator",
			wantAction:   "create",
		},
		{
			name:         "list operators",
			procedure:    "/nis.v1.OperatorService/ListOperators",
			wantResource: "operator",
			wantAction:   "read",
		},
		{
			name:         "get operator",
			procedure:    "/nis.v1.OperatorService/GetOperator",
			wantResource: "operator",
			wantAction:   "read",
		},
		{
			name:         "delete operator",
			procedure:    "/nis.v1.OperatorService/DeleteOperator",
			wantResource: "operator",
			wantAction:   "delete",
		},
		{
			name:         "update operator",
			procedure:    "/nis.v1.OperatorService/UpdateOperator",
			wantResource: "operator",
			wantAction:   "update",
		},
		{
			name:         "create account",
			procedure:    "/nis.v1.AccountService/CreateAccount",
			wantResource: "account",
			wantAction:   "create",
		},
		{
			name:         "list accounts",
			procedure:    "/nis.v1.AccountService/ListAccounts",
			wantResource: "account",
			wantAction:   "read",
		},
		{
			name:         "create user",
			procedure:    "/nis.v1.UserService/CreateUser",
			wantResource: "user",
			wantAction:   "create",
		},
		{
			name:         "list users",
			procedure:    "/nis.v1.UserService/ListUsers",
			wantResource: "user",
			wantAction:   "read",
		},
		{
			name:         "create cluster",
			procedure:    "/nis.v1.ClusterService/CreateCluster",
			wantResource: "cluster",
			wantAction:   "create",
		},

		// Special case: AuthService with ApiUser methods -> api_user resource
		{
			name:         "auth service create api user",
			procedure:    "/nis.v1.AuthService/CreateApiUser",
			wantResource: "api_user",
			wantAction:   "create",
		},
		{
			name:         "auth service list api users",
			procedure:    "/nis.v1.AuthService/ListApiUsers",
			wantResource: "api_user",
			wantAction:   "read",
		},
		{
			name:         "auth service delete api user",
			procedure:    "/nis.v1.AuthService/DeleteApiUser",
			wantResource: "api_user",
			wantAction:   "delete",
		},

		// Special case: AuthService non-apiuser method stays as auth
		{
			name:         "auth service login",
			procedure:    "/nis.v1.AuthService/Login",
			wantResource: "auth",
			wantAction:   "read",
		},

		// Special case: ScopedSigningKeyService -> scoped_key resource
		{
			name:         "scoped signing key create",
			procedure:    "/nis.v1.ScopedsigningkeyService/CreateScopedSigningKey",
			wantResource: "scoped_key",
			wantAction:   "create",
		},
		{
			name:         "scoped signing key list",
			procedure:    "/nis.v1.ScopedsigningkeyService/ListScopedSigningKeys",
			wantResource: "scoped_key",
			wantAction:   "read",
		},

		// Edge cases
		{
			name:         "empty procedure",
			procedure:    "",
			wantResource: "",
			wantAction:   "",
		},
		{
			name:         "procedure with only one part",
			procedure:    "NoProcedure",
			wantResource: "",
			wantAction:   "",
		},
		{
			name:         "procedure with two parts",
			procedure:    "/SomeService",
			wantResource: "",
			wantAction:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResource, gotAction := extractResourceAndAction(tt.procedure)
			assert.Equal(t, tt.wantResource, gotResource, "resource mismatch")
			assert.Equal(t, tt.wantAction, gotAction, "action mismatch")
		})
	}
}

func TestGetUserFromContext(t *testing.T) {
	t.Run("user present in context", func(t *testing.T) {
		expectedUser := &entities.APIUser{
			ID:       uuid.New(),
			Username: "testuser",
			Role:     entities.RoleAdmin,
		}
		ctx := context.WithValue(context.Background(), UserContextKey, expectedUser)

		user, ok := GetUserFromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, expectedUser, user)
		assert.Equal(t, "testuser", user.Username)
		assert.Equal(t, entities.RoleAdmin, user.Role)
	})

	t.Run("no user in context", func(t *testing.T) {
		ctx := context.Background()

		user, ok := GetUserFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, user)
	})

	t.Run("wrong type in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserContextKey, "not-a-user")

		user, ok := GetUserFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, user)
	})

	t.Run("different context key", func(t *testing.T) {
		expectedUser := &entities.APIUser{
			ID:       uuid.New(),
			Username: "testuser",
			Role:     entities.RoleOperatorAdmin,
		}
		ctx := context.WithValue(context.Background(), contextKey("other-key"), expectedUser)

		user, ok := GetUserFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, user)
	})
}
