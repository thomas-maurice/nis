package nats

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

func TestGenerateServerConfig(t *testing.T) {
	cfg := ServerConfig{
		Port:                4222,
		HTTPPort:            8222,
		ClusterName:         "test-cluster",
		ClusterPort:         6222,
		Routes:              []string{"nats://server1:6222", "nats://server2:6222"},
		OperatorJWT:         "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.operator.jwt",
		SystemAccountPubKey: "AABC123",
		ResolverPreload: map[string]string{
			"AABC123": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.account.jwt",
		},
	}

	config, err := GenerateServerConfig(cfg)
	require.NoError(t, err)
	assert.NotEmpty(t, config)

	// Verify key elements are present
	assert.Contains(t, config, "port: 4222")
	assert.Contains(t, config, "http_port: 8222")
	assert.Contains(t, config, "name: test-cluster")
	assert.Contains(t, config, "port: 6222")
	assert.Contains(t, config, "operator: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.operator.jwt")
	assert.Contains(t, config, "system_account: AABC123")
	assert.Contains(t, config, "type: full")
	assert.Contains(t, config, "allow_delete: true")
}

func TestGenerateServerConfig_Minimal(t *testing.T) {
	cfg := ServerConfig{
		Port:        4222,
		HTTPPort:    8222,
		OperatorJWT: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.operator.jwt",
	}

	config, err := GenerateServerConfig(cfg)
	require.NoError(t, err)
	assert.NotEmpty(t, config)

	// Verify minimal config
	assert.Contains(t, config, "port: 4222")
	assert.Contains(t, config, "http_port: 8222")
	assert.Contains(t, config, "operator: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.operator.jwt")

	// Should not contain cluster info
	assert.NotContains(t, config, "cluster {")
}

func TestGenerateServerConfigForCluster(t *testing.T) {
	// Create test entities
	operator := &entities.Operator{
		ID:          uuid.New(),
		Name:        "Test Operator",
		PublicKey:   "OABC123",
		JWT:         "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.operator.jwt",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	cluster := &entities.Cluster{
		ID:                  uuid.New(),
		Name:                "test-cluster",
		ServerURLs:          []string{"nats://server1:4222"},
		OperatorID:          operator.ID,
		SystemAccountPubKey: "AABC123",
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	accounts := []*entities.Account{
		{
			ID:         uuid.New(),
			OperatorID: operator.ID,
			Name:       "Account 1",
			PublicKey:  "AABC123",
			JWT:        "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.account1.jwt",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		},
		{
			ID:         uuid.New(),
			OperatorID: operator.ID,
			Name:       "Account 2",
			PublicKey:  "ADEF456",
			JWT:        "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.account2.jwt",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		},
	}

	config, err := GenerateServerConfigForCluster(cluster, operator, accounts, 4222, 8222)
	require.NoError(t, err)
	assert.NotEmpty(t, config)

	// Verify config contains expected elements
	assert.Contains(t, config, "port: 4222")
	assert.Contains(t, config, "http_port: 8222")
	assert.Contains(t, config, "name: test-cluster")
	assert.Contains(t, config, "system_account: AABC123")
	assert.Contains(t, config, operator.JWT)

	// Verify both accounts are preloaded
	assert.Contains(t, config, "AABC123")
	assert.Contains(t, config, "ADEF456")
}

func TestGenerateServerConfig_ValidFormat(t *testing.T) {
	cfg := ServerConfig{
		Port:        4222,
		HTTPPort:    8222,
		OperatorJWT: "test.jwt.token",
	}

	config, err := GenerateServerConfig(cfg)
	require.NoError(t, err)

	// Verify it's valid NATS config format (basic checks)
	lines := strings.Split(config, "\n")
	assert.Greater(t, len(lines), 5) // Should have multiple lines

	// Check for proper structure
	assert.True(t, strings.Contains(config, "port:"))
	assert.True(t, strings.Contains(config, "operator:"))
	assert.True(t, strings.Contains(config, "resolver {"))
}
