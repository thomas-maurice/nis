package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	Server            ServerConfig
	Database          DatabaseConfig
	Encryption        EncryptionConfig
	Auth              AuthConfig
	JetStreamDefaults JetStreamDefaults
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Host string
	Port int
}

// DatabaseConfig holds database connection configuration
type DatabaseConfig struct {
	Driver   string // "sqlite" or "postgres"
	Path     string // SQLite path
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// EncryptionConfig holds encryption key configuration
type EncryptionConfig struct {
	Keys         []EncryptionKey
	CurrentKeyID string // ID of the key to use for new encryptions
}

// EncryptionKey represents a single encryption key
type EncryptionKey struct {
	ID  string
	Key string // base64 encoded 32-byte key
}

// AuthConfig holds authentication and authorization configuration
type AuthConfig struct {
	SigningKeyPath   string
	TokenExpiry      time.Duration
	CasbinModelPath  string
	CasbinPolicyPath string
}

// JetStreamDefaults holds default JetStream limits for new accounts
type JetStreamDefaults struct {
	MaxMemory    int64
	MaxStorage   int64
	MaxStreams   int64
	MaxConsumers int64
}

// Load reads configuration from a file and environment variables
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set config file path
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
	}

	// Set defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.path", "./nis.db")
	v.SetDefault("auth.token_expiry", "24h")
	v.SetDefault("auth.casbin_model_path", "./config/casbin_model.conf")
	v.SetDefault("auth.casbin_policy_path", "./config/casbin_policy.csv")
	v.SetDefault("jetstream_defaults.max_memory", 1073741824)    // 1GB
	v.SetDefault("jetstream_defaults.max_storage", 10737418240)  // 10GB
	v.SetDefault("jetstream_defaults.max_streams", 10)
	v.SetDefault("jetstream_defaults.max_consumers", 100)

	// Read environment variables
	v.SetEnvPrefix("NIS")
	v.AutomaticEnv()

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found is acceptable, we'll use defaults
	}

	// Unmarshal config
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Database.Driver != "sqlite" && c.Database.Driver != "postgres" {
		return fmt.Errorf("invalid database driver: %s (must be 'sqlite' or 'postgres')", c.Database.Driver)
	}

	if c.Database.Driver == "sqlite" && c.Database.Path == "" {
		return fmt.Errorf("database.path is required for SQLite")
	}

	if c.Database.Driver == "postgres" {
		if c.Database.Host == "" {
			return fmt.Errorf("database.host is required for PostgreSQL")
		}
		if c.Database.DBName == "" {
			return fmt.Errorf("database.dbname is required for PostgreSQL")
		}
	}

	if len(c.Encryption.Keys) == 0 {
		return fmt.Errorf("at least one encryption key is required")
	}

	if c.Encryption.CurrentKeyID == "" {
		return fmt.Errorf("encryption.current_key_id is required")
	}

	// Validate that CurrentKeyID exists in Keys
	currentKeyExists := false
	for i, key := range c.Encryption.Keys {
		if key.ID == "" {
			return fmt.Errorf("encryption key %d is missing ID", i)
		}
		if key.Key == "" {
			return fmt.Errorf("encryption key %s is missing key value", key.ID)
		}
		if key.ID == c.Encryption.CurrentKeyID {
			currentKeyExists = true
		}
	}

	if !currentKeyExists {
		return fmt.Errorf("current_key_id '%s' does not exist in encryption keys", c.Encryption.CurrentKeyID)
	}

	if c.Auth.SigningKeyPath == "" {
		return fmt.Errorf("auth.signing_key_path is required")
	}

	if c.Auth.TokenExpiry <= 0 {
		return fmt.Errorf("auth.token_expiry must be positive")
	}

	return nil
}
