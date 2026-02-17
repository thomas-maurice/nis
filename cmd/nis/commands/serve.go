package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	grpcServer "github.com/thomas-maurice/nis/internal/interfaces/grpc"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the NATS Identity Service gRPC server",
	Long: `Start the NATS Identity Service gRPC server.

The server provides a gRPC API for managing NATS operators, accounts,
users, scoped signing keys, and clusters. It handles JWT generation,
encryption of sensitive data, and authentication/authorization.`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)

	// Server flags
	serveCmd.Flags().String("address", ":8080", "gRPC server listen address")
	serveCmd.Flags().String("db-driver", "sqlite", "database driver (sqlite or postgres)")
	serveCmd.Flags().String("db-dsn", "nis.db", "database connection string")
	serveCmd.Flags().String("encryption-key", "", "encryption key for sensitive data (exactly 32 bytes, base64 encoded recommended)")
	serveCmd.Flags().String("encryption-key-id", "default", "ID for the encryption key (useful for key rotation)")
	serveCmd.Flags().String("jwt-secret", "", "JWT signing secret (minimum 32 bytes recommended)")
	serveCmd.Flags().Duration("jwt-ttl", 24*time.Hour, "JWT token TTL")
	serveCmd.Flags().Bool("auto-migrate", true, "automatically run database migrations on startup")
	serveCmd.Flags().Bool("enable-ui", true, "enable web UI")

	// Bind flags to viper
	_ = viper.BindPFlag("server.address", serveCmd.Flags().Lookup("address"))
	_ = viper.BindPFlag("database.driver", serveCmd.Flags().Lookup("db-driver"))
	_ = viper.BindPFlag("database.dsn", serveCmd.Flags().Lookup("db-dsn"))
	_ = viper.BindPFlag("encryption.key", serveCmd.Flags().Lookup("encryption-key"))
	_ = viper.BindPFlag("encryption.key_id", serveCmd.Flags().Lookup("encryption-key-id"))
	_ = viper.BindPFlag("auth.jwt_secret", serveCmd.Flags().Lookup("jwt-secret"))
	_ = viper.BindPFlag("auth.jwt_ttl", serveCmd.Flags().Lookup("jwt-ttl"))
	_ = viper.BindPFlag("database.auto_migrate", serveCmd.Flags().Lookup("auto-migrate"))
	_ = viper.BindPFlag("server.enable_ui", serveCmd.Flags().Lookup("enable-ui"))

	// Note: encryption-key and jwt-secret are NOT marked as required flags
	// because they can be provided via config file or environment variables
}

func runServe(cmd *cobra.Command, args []string) error {
	// Get configuration from viper
	address := viper.GetString("server.address")
	dbDriver := viper.GetString("database.driver")
	dbDSN := viper.GetString("database.dsn")
	jwtSecret := viper.GetString("auth.jwt_secret")
	jwtTTL := viper.GetDuration("auth.jwt_ttl")
	autoMigrate := viper.GetBool("database.auto_migrate")
	enableUI := viper.GetBool("server.enable_ui")

	// Validate required configuration
	if jwtSecret == "" {
		return fmt.Errorf("JWT secret is required (--jwt-secret or AUTH_JWT_SECRET)")
	}

	// Create repository factory
	repoFactory, err := persistence.NewRepositoryFactory(persistence.Config{
		Driver: dbDriver,
		DSN:    dbDSN,
	})
	if err != nil {
		return fmt.Errorf("failed to create repository factory: %w", err)
	}

	// Connect to database
	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = repoFactory.Close() }()

	// Run migrations if enabled
	migrationsDone := false
	if autoMigrate {
		fmt.Println("Running database migrations...")
		if err := repoFactory.Migrate(ctx); err != nil {
			return fmt.Errorf("failed to run migrations: %w", err)
		}
		fmt.Println("Migrations completed successfully")
		migrationsDone = true
	} else {
		// Assume migrations are done if auto-migrate is disabled
		migrationsDone = true
	}

	// Initialize encryption service
	encryptor, err := initEncryptionService()
	if err != nil {
		return fmt.Errorf("failed to initialize encryption: %w", err)
	}

	// Initialize Casbin enforcer
	enforcer, err := initCasbin()
	if err != nil {
		return fmt.Errorf("failed to initialize Casbin: %w", err)
	}

	// Initialize JWT service
	jwtService := services.NewJWTService(encryptor)

	// Initialize business services using repository factory
	// Note: accountService must be created before operatorService because
	// operator creation uses accountService to create the $SYS account
	accountService := services.NewAccountService(
		repoFactory.AccountRepository(),
		repoFactory.OperatorRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		jwtService,
		encryptor,
	)

	operatorService := services.NewOperatorService(
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
		accountService,
		jwtService,
		encryptor,
	)

	userService := services.NewUserService(
		repoFactory.UserRepository(),
		repoFactory.AccountRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		jwtService,
		encryptor,
	)

	scopedKeyService := services.NewScopedSigningKeyService(
		repoFactory.ScopedSigningKeyRepository(),
		repoFactory.AccountRepository(),
		encryptor,
	)

	clusterService := services.NewClusterService(
		repoFactory.ClusterRepository(),
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		encryptor,
		jwtService,
	)

	authService := services.NewAuthService(
		repoFactory.APIUserRepository(),
		jwtSecret,
		jwtTTL,
	)

	exportService := services.NewExportService(
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		repoFactory.ClusterRepository(),
		operatorService,
		accountService,
		userService,
		scopedKeyService,
		clusterService,
		encryptor,
	)

	// Initialize permission service for scope-based access control
	permissionService := services.NewPermissionService(
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
	)

	// Initialize auth middleware
	authMiddleware := middleware.NewAuthInterceptor(authService, enforcer)

	// Initialize gRPC server with auth middleware
	server := grpcServer.NewServer(
		grpcServer.ServerConfig{
			Address:        address,
			EnableUI:       enableUI,
			MigrationsDone: migrationsDone,
		},
		operatorService,
		accountService,
		userService,
		scopedKeyService,
		clusterService,
		authService,
		exportService,
		permissionService,
		authMiddleware,
	)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start cluster health check goroutine
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		// Do an initial health check after 5 seconds
		time.Sleep(5 * time.Second)
		if err := clusterService.CheckAllClustersHealth(ctx); err != nil {
			fmt.Printf("Health check error: %v\n", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := clusterService.CheckAllClustersHealth(ctx); err != nil {
					fmt.Printf("Health check error: %v\n", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		fmt.Printf("Starting NATS Identity Service on %s\n", address)
		fmt.Println("Cluster health checks will run every 60 seconds")
		if err := server.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		fmt.Println("\nReceived shutdown signal, gracefully shutting down...")
		cancel()
		return server.Shutdown()
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		return server.Shutdown()
	}
}

func initEncryptionService() (encryption.Encryptor, error) {
	// Try to load encryption keys from config file first
	var encryptionKeys []struct {
		ID  string
		Key string
	}
	if err := viper.UnmarshalKey("encryption.keys", &encryptionKeys); err == nil && len(encryptionKeys) > 0 {
		// Config file has encryption keys defined
		currentKeyID := viper.GetString("encryption.current_key_id")
		if currentKeyID == "" {
			return nil, fmt.Errorf("encryption.current_key_id is required when using encryption.keys in config")
		}

		// Build key map
		keys := make(map[string]string)
		for _, k := range encryptionKeys {
			if k.ID == "" {
				return nil, fmt.Errorf("encryption key is missing ID")
			}
			if k.Key == "" {
				return nil, fmt.Errorf("encryption key %s is missing key value", k.ID)
			}
			keys[k.ID] = k.Key
		}

		// Verify current key exists
		if _, ok := keys[currentKeyID]; !ok {
			return nil, fmt.Errorf("current_key_id '%s' does not exist in encryption keys", currentKeyID)
		}

		encryptor, err := encryption.NewChaChaEncryptor(keys, currentKeyID)
		if err != nil {
			return nil, fmt.Errorf("failed to create encryptor: %w", err)
		}

		fmt.Printf("Loaded %d encryption key(s) from config (current: %s)\n", len(keys), currentKeyID)
		return encryptor, nil
	}

	// Fall back to single encryption key from flag or environment variable
	encryptionKey := viper.GetString("encryption.key")
	if encryptionKey == "" {
		return nil, fmt.Errorf("encryption key is required (--encryption-key flag, encryption.key config, or ENCRYPTION_KEY environment variable)")
	}

	// Get the key ID (defaults to "default" if not specified)
	keyID := viper.GetString("encryption.key_id")
	if keyID == "" {
		keyID = "default"
	}

	// Ensure key is 32 bytes for ChaCha20-Poly1305
	if len(encryptionKey) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes, got %d bytes", len(encryptionKey))
	}

	// NewChaChaEncryptor expects a map of base64-encoded keys
	encodedKey := base64.StdEncoding.EncodeToString([]byte(encryptionKey))

	keys := map[string]string{
		keyID: encodedKey,
	}

	encryptor, err := encryption.NewChaChaEncryptor(keys, keyID)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	fmt.Printf("Using encryption key with ID: %s\n", keyID)
	return encryptor, nil
}

func initCasbin() (*casbin.Enforcer, error) {
	// Load Casbin model and policy from embedded files in services package
	modelPath := "internal/application/services/casbin_model.conf"
	policyPath := "internal/application/services/casbin_policy.csv"

	enforcer, err := casbin.NewEnforcer(modelPath, policyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create Casbin enforcer: %w", err)
	}

	return enforcer, nil
}
