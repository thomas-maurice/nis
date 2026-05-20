package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/tracing"
	grpcServer "github.com/thomas-maurice/nis/internal/interfaces/grpc"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
)

// serverVersion returns the version string set via SetVersion at startup,
// falling back to "dev" so the OTel service.version attribute and Prometheus
// labels still have a value during local runs.
func serverVersion() string {
	if v := rootCmd.Version; v != "" {
		return v
	}
	return "dev"
}

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

	// Observability flags. Prometheus /metrics is on by default and zero-cost
	// when nothing scrapes it. OTel tracing is off by default — turning it on
	// without a configured collector would log export errors every batch.
	serveCmd.Flags().Bool("metrics-enabled", true, "expose Prometheus /metrics and record OTel metrics")
	serveCmd.Flags().Bool("tracing-enabled", false, "export OpenTelemetry traces over OTLP/gRPC")
	serveCmd.Flags().String("tracing-endpoint", "localhost:4317", "OTLP/gRPC collector endpoint (host:port)")
	serveCmd.Flags().Bool("tracing-insecure", true, "disable TLS for the OTLP connection")
	serveCmd.Flags().Float64("tracing-sample-ratio", 1.0, "TraceIDRatio sampler ratio in [0,1]")
	serveCmd.Flags().String("tracing-service-name", "nis", "service.name OpenTelemetry resource attribute")

	// Events + webhook worker flags.
	serveCmd.Flags().Int("events-retention-days", 30, "retention for the events table; 0 disables cleanup")
	serveCmd.Flags().Int("webhooks-succeeded-retention-days", 7, "retention for succeeded webhook_deliveries; dead-letter rows are never auto-deleted")
	serveCmd.Flags().Int("webhooks-poll-interval-seconds", 0, "delivery worker poll cadence in seconds; 0 = auto (2s Postgres, 10s SQLite)")
	serveCmd.Flags().Int("webhooks-delivery-timeout-seconds", 10, "per-POST HTTP timeout in seconds")
	serveCmd.Flags().Int("webhooks-max-attempts", 5, "deliveries become dead_letter after this many failed attempts")
	serveCmd.Flags().Int("webhooks-backoff-base-seconds", 10, "exponential-backoff base interval in seconds")
	serveCmd.Flags().Int("webhooks-backoff-cap-seconds", 600, "exponential-backoff cap interval in seconds")
	serveCmd.Flags().Int("webhooks-shutdown-timeout-seconds", 30, "graceful drain of in-flight deliveries on SIGTERM")

	// Jobs substrate flags (A2). Replaces the standalone EventsRetentionWorker
	// loop; future scheduled work (P12 backups, A14 JWT sweeps, A15 cluster
	// health) lands on this same runner.
	serveCmd.Flags().Int("jobs-poll-interval-seconds", 0, "job runner poll cadence in seconds; 0 = auto (2s Postgres, 10s SQLite)")
	serveCmd.Flags().Int("jobs-claim-batch", 10, "rows claimed per poll tick")
	serveCmd.Flags().Int("jobs-lease-duration-seconds", 300, "how long a claim holds a row before a dead worker's row can be reclaimed")
	serveCmd.Flags().Int("jobs-shutdown-timeout-seconds", 30, "graceful drain of in-flight jobs on SIGTERM")
	serveCmd.Flags().Int("jobs-retention-days", 30, "retention for succeeded + cancelled job rows; dead-letter + failed survive forever")

	// Flags are wired into viper via applyFlagOverrides in runServe rather
	// than viper.BindPFlag — see cmd/nis/commands/viper_overrides.go for why.
	// Note: encryption-key and jwt-secret are NOT marked as required flags
	// because they can be provided via config file or environment variables.
}

// serveFlagMapping maps cobra flag names to viper config keys for the serve
// command. Flags listed here only override viper values when explicitly
// passed (avoids the flag-default-shadows-config trap).
var serveFlagMapping = map[string]string{
	"address":               "server.address",
	"db-driver":             "database.driver",
	"db-dsn":                "database.dsn",
	"encryption-key":        "encryption.key",
	"encryption-key-id":     "encryption.key_id",
	"jwt-secret":            "auth.jwt_secret",
	"jwt-ttl":               "auth.jwt_ttl",
	"auto-migrate":          "database.auto_migrate",
	"enable-ui":             "server.enable_ui",
	"metrics-enabled":       "metrics.enabled",
	"tracing-enabled":       "tracing.enabled",
	"tracing-endpoint":      "tracing.endpoint",
	"tracing-insecure":      "tracing.insecure",
	"tracing-sample-ratio":  "tracing.sample_ratio",
	"tracing-service-name":  "tracing.service_name",

	"events-retention-days":             "events.retention_days",
	"webhooks-succeeded-retention-days": "webhooks.succeeded_retention_days",
	"webhooks-poll-interval-seconds":    "webhooks.poll_interval_seconds",
	"webhooks-delivery-timeout-seconds": "webhooks.delivery_timeout_seconds",
	"webhooks-max-attempts":             "webhooks.max_attempts",
	"webhooks-backoff-base-seconds":     "webhooks.backoff_base_seconds",
	"webhooks-backoff-cap-seconds":      "webhooks.backoff_cap_seconds",
	"webhooks-shutdown-timeout-seconds": "webhooks.shutdown_timeout_seconds",

	"jobs-poll-interval-seconds":    "jobs.poll_interval_seconds",
	"jobs-claim-batch":              "jobs.claim_batch",
	"jobs-lease-duration-seconds":   "jobs.lease_duration_seconds",
	"jobs-shutdown-timeout-seconds": "jobs.shutdown_timeout_seconds",
	"jobs-retention-days":           "jobs.retention_days",
}

func runServe(cmd *cobra.Command, args []string) error {
	applyFlagOverrides(cmd, serveFlagMapping)

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

	logger := logging.GetLogger()

	// Run migrations if enabled
	migrationsDone := false
	if autoMigrate {
		logger.Info("running database migrations")
		if err := repoFactory.Migrate(ctx); err != nil {
			return fmt.Errorf("failed to run migrations: %w", err)
		}
		logger.Info("migrations completed successfully")
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

	// Initialize observability (metrics + optional tracing). Both default to
	// safe values — metrics on, tracing off — so the binary boots without an
	// OTel collector present. See README "Observability" for setup guidance.
	metricsProvider, metricsHandler, err := maybeInitMetrics()
	if err != nil {
		return fmt.Errorf("failed to initialize metrics: %w", err)
	}
	defer func() {
		if metricsProvider != nil {
			_ = metricsProvider.Shutdown(context.Background())
		}
	}()

	tracingShutdown, err := maybeInitTracing(context.Background())
	if err != nil {
		return fmt.Errorf("failed to initialize tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracingShutdown(shutdownCtx)
	}()

	// Register domain gauges and start the periodic refresh. Inventory queries
	// cost a handful of COUNT(*); running them at 60s cadence stays well below
	// Prometheus' typical scrape interval without paying per-scrape.
	var domainGauges *metrics.DomainGauges
	if metricsProvider != nil {
		domainGauges, err = metrics.RegisterDomainGauges(repoFactory)
		if err != nil {
			return fmt.Errorf("failed to register domain gauges: %w", err)
		}
	}

	// Initialize Casbin enforcer
	enforcer, err := initCasbin()
	if err != nil {
		return fmt.Errorf("failed to initialize Casbin: %w", err)
	}

	// Initialize JWT service
	jwtService := services.NewJWTService(encryptor)

	// Initialize business services. The multi-write ones (Account, Operator,
	// ScopedSigningKey) take the repository factory directly so they can open
	// transactions via factory.WithTx for atomic multi-row work. Note:
	// accountService must be created before operatorService because operator
	// creation uses accountService.createAccountTx to create the $SYS account
	// inside the same tx.
	accountService := services.NewAccountService(repoFactory, jwtService, encryptor)

	operatorService := services.NewOperatorService(repoFactory, accountService, jwtService, encryptor)

	userService := services.NewUserService(
		repoFactory.UserRepository(),
		repoFactory.AccountRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		repoFactory.OperatorRepository(),
		jwtService,
		encryptor,
	).WithFactory(repoFactory)

	scopedKeyService := services.NewScopedSigningKeyService(repoFactory, jwtService, encryptor)

	clusterService := services.NewClusterService(
		repoFactory.ClusterRepository(),
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
		repoFactory.ScopedSigningKeyRepository(),
		encryptor,
		jwtService,
	).WithFactory(repoFactory)

	// Account deletion must propagate to the NATS resolver — without this
	// wire, a deleted account's JWT would stay on the resolver until manual
	// `nisctl cluster sync --prune` and any previously-issued .creds would
	// keep connecting indefinitely. Done post-construction because
	// AccountService doesn't otherwise need a ClusterService dep.
	accountService.WithClusterService(clusterService)
	// P6 auto-sync: SKK mutations also push the parent account JWT to
	// every cluster after the tx commits. Without this, an operator who
	// adds a scoped key or bumps a template would have to remember to
	// `nisctl cluster sync` afterwards. Same best-effort semantic as
	// AccountService; failures logged but not propagated.
	scopedKeyService.WithClusterService(clusterService)

	authService := services.NewAuthService(
		repoFactory.APIUserRepository(),
		jwtSecret,
		jwtTTL,
	)

	exportService := services.NewExportService(
		repoFactory,
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

	eventService := services.NewEventService(repoFactory)

	// Jobs substrate (A2) read/admin surface — separate from JobRunner
	// which executes work. Admin-only at the handler layer.
	jobService := services.NewJobService(repoFactory)

	webhookService := services.NewWebhookService(repoFactory, encryptor)

	apiTokenService := services.NewAPITokenService(repoFactory)

	// Initialize permission service for scope-based access control
	permissionService := services.NewPermissionService(
		repoFactory.OperatorRepository(),
		repoFactory.AccountRepository(),
		repoFactory.UserRepository(),
	)

	// Global search (P11) — narrows results to caller's RBAC scope before
	// returning, so cross-operator isolation is enforced regardless of what
	// the LIKE query matched in the raw repos.
	searchService := services.NewSearchService(repoFactory, permissionService)

	// Permission templates (P6) — operator-scoped versioned permission
	// bundles. The service owns CRUD + versioning + delete-blocked-by-
	// dependents; the actual application of a template version to an SKK
	// is in ScopedSigningKeyService.BumpScopedKeyTemplate so the existing
	// account-JWT regen + post-commit push path is reused.
	templateService := services.NewTemplateService(repoFactory, permissionService).
		WithSKKService(scopedKeyService)

	// Initialize auth middleware. The API-token flusher coalesces last_used_at
	// updates so every authenticated request doesn't trigger its own DB write —
	// burst CI traffic against SQLite would otherwise serialize behind those writes.
	apiTokenFlusher := middleware.NewAPITokenLastUsedFlusher(
		repoFactory.APITokenRepository(),
		time.Duration(viper.GetInt("api_tokens.last_used_flush_interval_seconds"))*time.Second,
	)
	authMiddleware := middleware.NewAuthInterceptor(authService, enforcer).
		WithAPITokenService(apiTokenService, apiTokenFlusher)

	// JWT lifecycle (P2) wiring: revocation service composes user mutations +
	// account JWT regen + cluster push; the sweeper drives prune / expiring-soon /
	// expired / auto-renew on the configured interval (and on demand via
	// OperatorService.RunJWTExpirySweep).
	userRevocationService := services.NewUserRevocationService(repoFactory, jwtService, clusterService, encryptor)
	jwtSweepInterval := time.Duration(viper.GetInt("jwt_policy.sweep_interval_seconds")) * time.Second
	jwtSweepBatch := viper.GetInt("jwt_policy.sweep_batch_limit")
	jwtExpirySweeper := services.NewJWTExpirySweeper(repoFactory, jwtService, userRevocationService, clusterService, jwtSweepInterval, jwtSweepBatch)

	// Initialize gRPC server with auth middleware
	server := grpcServer.NewServer(
		grpcServer.ServerConfig{
			Address:         address,
			EnableUI:        enableUI,
			MigrationsDone:  migrationsDone,
			RepoFactory:     repoFactory,
			Encryptor:       encryptor,
			MetricsProvider: metricsProvider,
		},
		operatorService,
		accountService,
		userService,
		scopedKeyService,
		clusterService,
		authService,
		exportService,
		eventService,
		webhookService,
		apiTokenService,
		userRevocationService,
		jwtExpirySweeper,
		searchService,
		templateService,
		permissionService,
		jobService,
		authMiddleware,
	)
	if metricsHandler != nil {
		server.AttachMetricsHandler(metricsHandler)
	}

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
			logger.Error("health check error", "error", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := clusterService.CheckAllClustersHealth(ctx); err != nil {
					logger.Error("health check error", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start domain gauge refresh loop. Single goroutine, 60s cadence.
	if domainGauges != nil {
		go domainGauges.RefreshLoop(ctx, 60*time.Second)
	}

	// Jobs substrate (A2). Replaces the dedicated EventsRetentionWorker
	// goroutine with two handlers — events.retention_sweep and
	// jobs.retention_sweep — registered on the JobRunner. The runner's
	// per-tick watchdog keeps both schedules healthy across handler
	// crashes, and the JobsView UI / JobService RPC surface gives admins
	// visibility into the schedule. A16 (webhook.deliver) is now also on
	// this substrate; A14/A15 remain in-process goroutines for now.
	jobsPollInterval := time.Duration(viper.GetInt("jobs.poll_interval_seconds")) * time.Second
	if jobsPollInterval == 0 {
		jobsPollInterval = 2 * time.Second
		if dbDriver == "sqlite" {
			jobsPollInterval = 10 * time.Second
		}
	}
	jobRunner := services.NewJobRunner(repoFactory, services.JobRunnerConfig{
		PollInterval:    jobsPollInterval,
		ClaimBatch:      viper.GetInt("jobs.claim_batch"),
		LeaseDuration:   time.Duration(viper.GetInt("jobs.lease_duration_seconds")) * time.Second,
		ShutdownTimeout: time.Duration(viper.GetInt("jobs.shutdown_timeout_seconds")) * time.Second,
	})
	services.RegisterRetentionHandlers(jobRunner, repoFactory, services.RetentionConfig{
		EventRetentionDays:             viper.GetInt("events.retention_days"),
		SucceededDeliveryRetentionDays: viper.GetInt("webhooks.succeeded_retention_days"),
		JobRetentionDays:               viper.GetInt("jobs.retention_days"),
		SweepInterval:                  24 * time.Hour,
	})
	// Webhook delivery (A16). Per-delivery jobs of type webhook.deliver;
	// fanoutDeliveries in the events package enqueues one alongside each
	// webhook_deliveries row inside the same tx (atomic enqueue).
	webhookMaxAttempts := viper.GetInt("webhooks.max_attempts")
	services.RegisterWebhookHandlers(jobRunner, repoFactory, encryptor, services.WebhookHandlerConfig{
		MaxAttempts:     webhookMaxAttempts,
		BackoffBase:     time.Duration(viper.GetInt("webhooks.backoff_base_seconds")) * time.Second,
		BackoffCap:      time.Duration(viper.GetInt("webhooks.backoff_cap_seconds")) * time.Second,
		DeliveryTimeout: time.Duration(viper.GetInt("webhooks.delivery_timeout_seconds")) * time.Second,
	})
	if viper.GetInt("webhooks.poll_interval_seconds") != 0 ||
		viper.GetInt("webhooks.shutdown_timeout_seconds") != 30 {
		logger.Info("webhook delivery now runs on jobs substrate; webhooks.poll_interval_seconds and webhooks.shutdown_timeout_seconds are ignored (use jobs.poll_interval_seconds and jobs.shutdown_timeout_seconds instead)")
	}
	go func() { _ = jobRunner.Run(ctx) }()

	// Catch up any non-terminal webhook_deliveries rows that don't already
	// have a job — covers (a) operators upgrading from the pre-A16 worker
	// with rows in-flight, and (b) the rare window where an EmitTx ran
	// before SetJobEnqueuer was installed. Async so a large backlog doesn't
	// block startup; the runner's claim path is race-free vs new enqueues
	// (dedup_key=delivery_id on the partial unique index).
	go func() {
		if _, err := services.EnqueueCatchUpDeliveries(ctx, repoFactory, webhookMaxAttempts); err != nil {
			logger.Error("webhook catch-up scan failed", "error", err)
		}
	}()

	// JWT expiry sweeper (P2). Off-by-default behaviour comes from the per-
	// operator policy (TTL=0 means the sweeper finds nothing to do). When an
	// operator opts in, this loop emits expiring-soon/expired events, optionally
	// auto-renews, and prunes the parent account's Revocations map.
	go jwtExpirySweeper.Run(ctx)

	apiTokenFlusher.Start(ctx)
	defer apiTokenFlusher.Stop()

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		logger.Info("starting NATS Identity Service",
			"address", address, "health_check_interval", "60s")
		if err := server.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		logger.Info("received shutdown signal, gracefully shutting down")
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

		logging.GetLogger().Info("loaded encryption keys from config",
			"count", len(keys), "current_key_id", currentKeyID)
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

	logging.GetLogger().Info("using encryption key", "key_id", keyID)
	return encryptor, nil
}

func initCasbin() (*casbin.Enforcer, error) {
	// Model and policy are embedded in the services package via //go:embed,
	// so this works regardless of the binary's launch directory.
	return services.NewCasbinEnforcer()
}

// maybeInitMetrics returns (nil, nil, nil) when metrics are disabled in
// config, otherwise builds an OTel meter provider backed by a Prometheus
// registry and returns the provider + a handler for /metrics.
func maybeInitMetrics() (*metrics.Provider, http.Handler, error) {
	if !viper.GetBool("metrics.enabled") {
		return nil, nil, nil
	}
	p, handler, err := metrics.New("nis", serverVersion())
	if err != nil {
		return nil, nil, err
	}
	logging.GetLogger().Info("metrics enabled", "endpoint", "/metrics")
	return p, handler, nil
}

// maybeInitTracing returns a no-op shutdown when tracing is disabled. When
// enabled it stands up an OTLP/gRPC exporter pointed at tracing.endpoint.
func maybeInitTracing(ctx context.Context) (tracing.ShutdownFunc, error) {
	cfg := tracing.Config{
		Enabled:  viper.GetBool("tracing.enabled"),
		Endpoint: viper.GetString("tracing.endpoint"),
		Insecure: viper.GetBool("tracing.insecure"),
		Service:  viper.GetString("tracing.service_name"),
		Version:  serverVersion(),
		Sampler:  viper.GetFloat64("tracing.sample_ratio"),
	}
	shutdown, err := tracing.Init(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Enabled {
		logging.GetLogger().Info("tracing enabled",
			"endpoint", cfg.Endpoint,
			"insecure", cfg.Insecure,
			"sample_ratio", cfg.Sampler)
	}
	return shutdown, nil
}
