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

// The previous TestExtractAction / TestExtractResourceAndAction tests are gone:
// the verb-prefix heuristic they covered was deleted in A17 (2026-05-23) in
// favour of an explicit registry lookup. Coverage of the lookup itself lives
// in internal/application/authz/registry_test.go.

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
