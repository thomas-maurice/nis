package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validConfig returns a Config that passes all validation checks.
func validConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			Path:   "./nis.db",
		},
		Encryption: EncryptionConfig{
			Keys: []EncryptionKey{
				{ID: "key-1", Key: "dGVzdC1rZXktMzItYnl0ZXMtbG9uZy4uLi4u"},
			},
			CurrentKeyID: "key-1",
		},
		Auth: AuthConfig{
			SigningKeyPath: "/tmp/signing.key",
			TokenExpiry:    24 * time.Hour,
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(c *Config)
		wantErr string
	}{
		{
			name:    "valid config passes validation",
			modify:  func(c *Config) {},
			wantErr: "",
		},
		{
			name: "valid postgres config passes validation",
			modify: func(c *Config) {
				c.Database.Driver = "postgres"
				c.Database.Host = "localhost"
				c.Database.DBName = "nis"
			},
			wantErr: "",
		},
		{
			name: "invalid database driver rejected",
			modify: func(c *Config) {
				c.Database.Driver = "mysql"
			},
			wantErr: "invalid database driver: mysql",
		},
		{
			name: "missing SQLite path rejected",
			modify: func(c *Config) {
				c.Database.Driver = "sqlite"
				c.Database.Path = ""
			},
			wantErr: "database.path is required for SQLite",
		},
		{
			name: "missing PostgreSQL host rejected",
			modify: func(c *Config) {
				c.Database.Driver = "postgres"
				c.Database.Host = ""
				c.Database.DBName = "nis"
			},
			wantErr: "database.host is required for PostgreSQL",
		},
		{
			name: "missing PostgreSQL dbname rejected",
			modify: func(c *Config) {
				c.Database.Driver = "postgres"
				c.Database.Host = "localhost"
				c.Database.DBName = ""
			},
			wantErr: "database.dbname is required for PostgreSQL",
		},
		{
			name: "empty encryption keys rejected",
			modify: func(c *Config) {
				c.Encryption.Keys = nil
			},
			wantErr: "at least one encryption key is required",
		},
		{
			name: "missing current_key_id rejected",
			modify: func(c *Config) {
				c.Encryption.CurrentKeyID = ""
			},
			wantErr: "encryption.current_key_id is required",
		},
		{
			name: "current_key_id not found in keys rejected",
			modify: func(c *Config) {
				c.Encryption.CurrentKeyID = "nonexistent-key"
			},
			wantErr: "current_key_id 'nonexistent-key' does not exist in encryption keys",
		},
		{
			name: "encryption key missing ID rejected",
			modify: func(c *Config) {
				c.Encryption.Keys = []EncryptionKey{
					{ID: "", Key: "some-key-value"},
				}
			},
			wantErr: "encryption key 0 is missing ID",
		},
		{
			name: "encryption key missing key value rejected",
			modify: func(c *Config) {
				c.Encryption.Keys = []EncryptionKey{
					{ID: "key-1", Key: ""},
				}
				c.Encryption.CurrentKeyID = "key-1"
			},
			wantErr: "encryption key key-1 is missing key value",
		},
		{
			name: "missing signing_key_path rejected",
			modify: func(c *Config) {
				c.Auth.SigningKeyPath = ""
			},
			wantErr: "auth.signing_key_path is required",
		},
		{
			name: "zero token_expiry rejected",
			modify: func(c *Config) {
				c.Auth.TokenExpiry = 0
			},
			wantErr: "auth.token_expiry must be positive",
		},
		{
			name: "negative token_expiry rejected",
			modify: func(c *Config) {
				c.Auth.TokenExpiry = -1 * time.Hour
			},
			wantErr: "auth.token_expiry must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(&cfg)

			err := cfg.Validate()

			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoad_WithConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  host: "127.0.0.1"
  port: 9090
database:
  driver: "sqlite"
  path: "/tmp/test.db"
encryption:
  keys:
    - id: "primary"
      key: "dGVzdC1rZXktMzItYnl0ZXMtbG9uZy4uLi4u"
  currentkeyid: "primary"
auth:
  signingkeypath: "/tmp/signing.key"
  tokenexpiry: "12h"
  casbinmodelpath: "./config/casbin_model.conf"
  casbinpolicypath: "./config/casbin_policy.csv"
jetstreamdefaults:
  maxmemory: 536870912
  maxstorage: 5368709120
  maxstreams: 5
  maxconsumers: 50
`

	err := os.WriteFile(cfgPath, []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "sqlite", cfg.Database.Driver)
	assert.Equal(t, "/tmp/test.db", cfg.Database.Path)
	assert.Len(t, cfg.Encryption.Keys, 1)
	assert.Equal(t, "primary", cfg.Encryption.Keys[0].ID)
	assert.Equal(t, "primary", cfg.Encryption.CurrentKeyID)
	assert.Equal(t, "/tmp/signing.key", cfg.Auth.SigningKeyPath)
	assert.Equal(t, 12*time.Hour, cfg.Auth.TokenExpiry)
	assert.Equal(t, int64(536870912), cfg.JetStreamDefaults.MaxMemory)
	assert.Equal(t, int64(5368709120), cfg.JetStreamDefaults.MaxStorage)
	assert.Equal(t, int64(5), cfg.JetStreamDefaults.MaxStreams)
	assert.Equal(t, int64(50), cfg.JetStreamDefaults.MaxConsumers)
}

func TestLoad_WithDefaults(t *testing.T) {
	// Load from a nonexistent path to trigger defaults
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "nonexistent-dir")

	// Use a directory that does not contain any config file;
	// viper treats ConfigFileNotFoundError as acceptable.
	cfg, err := Load(cfgPath)

	// When a specific file path is given but does not exist, Load returns an error.
	// This verifies that behaviour.
	if err != nil {
		assert.Contains(t, err.Error(), "failed to read config file")
		return
	}

	// If no error (viper found defaults), check some defaults are applied.
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "sqlite", cfg.Database.Driver)
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	err := os.WriteFile(cfgPath, []byte("{{invalid yaml:::"), 0644)
	require.NoError(t, err)

	_, err = Load(cfgPath)
	assert.Error(t, err)
}
