package grpc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/handlers"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
	httpInterface "github.com/thomas-maurice/nis/internal/interfaces/http"
)

// ServerConfig contains configuration for the gRPC server
type ServerConfig struct {
	Address        string
	EnableUI       bool
	MigrationsDone bool

	// RepoFactory is used by /readyz to probe the database.
	RepoFactory persistence.RepositoryFactory
	// Encryptor is used by /readyz to verify the encryption subsystem.
	Encryptor encryption.Encryptor
	// MetricsProvider, if set, exposes its /metrics handler and supplies the
	// recorder used by the HTTP middleware. Optional — when nil, /metrics is
	// not registered and only otelconnect-emitted RPC metrics are tracked
	// (which themselves are no-ops without a meter provider).
	MetricsProvider *metrics.Provider
}

// Server wraps the HTTP server for gRPC/ConnectRPC
type Server struct {
	config     ServerConfig
	httpServer *http.Server
	mux        *http.ServeMux
}

// NewServer creates a new gRPC server with all handlers wired up
func NewServer(
	config ServerConfig,
	operatorService *services.OperatorService,
	accountService *services.AccountService,
	userService *services.UserService,
	scopedKeyService *services.ScopedSigningKeyService,
	clusterService *services.ClusterService,
	authService *services.AuthService,
	exportService *services.ExportService,
	eventService *services.EventService,
	webhookService *services.WebhookService,
	apiTokenService *services.APITokenService,
	userRevocationService *services.UserRevocationService,
	jwtExpirySweeper *services.JWTExpirySweeper,
	searchService *services.SearchService,
	templateService *services.TemplateService,
	permService *services.PermissionService,
	jobService *services.JobService,
	backupService *services.BackupService,
	authInterceptor *middleware.AuthInterceptor,
) *Server {
	mux := http.NewServeMux()

	// Build interceptor chain. otelconnect must run before the auth interceptor
	// so that even rejected requests show up in rpc.server.duration with a
	// proper Connect code label.
	interceptors := []connect.Interceptor{}
	otelInterceptor, err := otelconnect.NewInterceptor(
		otelconnect.WithoutServerPeerAttributes(),
	)
	if err != nil {
		logging.GetLogger().Warn("metrics: failed to build otelconnect interceptor — RPC metrics will be missing", "error", err)
	} else {
		interceptors = append(interceptors, otelInterceptor)
	}
	interceptors = append(interceptors, authInterceptor)
	interceptorOption := connect.WithInterceptors(interceptors...)

	// Register all service handlers with auth interceptor
	operatorHandler := handlers.NewOperatorHandler(operatorService, permService, jwtExpirySweeper)
	mux.Handle(nisv1connect.NewOperatorServiceHandler(operatorHandler, interceptorOption))

	accountHandler := handlers.NewAccountHandler(accountService, permService)
	mux.Handle(nisv1connect.NewAccountServiceHandler(accountHandler, interceptorOption))

	userHandler := handlers.NewUserHandler(userService, permService, userRevocationService)
	mux.Handle(nisv1connect.NewUserServiceHandler(userHandler, interceptorOption))

	scopedKeyHandler := handlers.NewScopedSigningKeyHandler(scopedKeyService, permService)
	mux.Handle(nisv1connect.NewScopedSigningKeyServiceHandler(scopedKeyHandler, interceptorOption))

	clusterHandler := handlers.NewClusterHandler(clusterService, permService)
	mux.Handle(nisv1connect.NewClusterServiceHandler(clusterHandler, interceptorOption))

	authHandler := handlers.NewAuthHandler(authService)
	mux.Handle(nisv1connect.NewAuthServiceHandler(authHandler, interceptorOption))

	exportHandler := handlers.NewExportHandler(exportService, permService)
	mux.Handle(nisv1connect.NewExportServiceHandler(exportHandler, interceptorOption))

	eventHandler := handlers.NewEventHandler(eventService)
	mux.Handle(nisv1connect.NewEventServiceHandler(eventHandler, interceptorOption))

	webhookHandler := handlers.NewWebhookHandler(webhookService, permService)
	mux.Handle(nisv1connect.NewWebhookServiceHandler(webhookHandler, interceptorOption))

	apiTokenHandler := handlers.NewAPITokenHandler(apiTokenService, permService)
	mux.Handle(nisv1connect.NewAPITokenServiceHandler(apiTokenHandler, interceptorOption))

	searchHandler := handlers.NewSearchHandler(searchService)
	mux.Handle(nisv1connect.NewSearchServiceHandler(searchHandler, interceptorOption))

	templateHandler := handlers.NewTemplateHandler(templateService, scopedKeyService, permService, config.RepoFactory)
	mux.Handle(nisv1connect.NewTemplateServiceHandler(templateHandler, interceptorOption))

	jobHandler := handlers.NewJobHandler(jobService)
	mux.Handle(nisv1connect.NewJobServiceHandler(jobHandler, interceptorOption))

	// BackupHandler is wired unconditionally — when backupService is nil
	// (backups.enabled=false) the handler short-circuits every RPC with
	// FailedPrecondition. Keeping the route registered means a config flip
	// doesn't require a restart for clients to start succeeding on the
	// same address.
	backupHandler := handlers.NewBackupHandler(backupService, operatorService, permService)
	mux.Handle(nisv1connect.NewBackupServiceHandler(backupHandler, interceptorOption))

	// /livez — process is alive. Always 200. Use this for k8s liveness probes.
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// /healthz — back-compat: same lax semantics as before (200 when migrations
	// have run, 503 otherwise). The Dockerfile HEALTHCHECK, docker-compose, and
	// the OPERATIONS.md Prometheus alert all consume this endpoint; making it
	// stricter would cause restart loops on transient DB hiccups. For strict
	// readiness, use /readyz.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if config.MigrationsDone {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("migrations pending"))
		}
	})

	// /readyz — strict: migrations + DB ping + encryptor self-test. Returns a
	// JSON body so operators can see which component failed. Use this for k8s
	// readiness probes and Prometheus blackbox checks.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		handleReadyz(w, r, config)
	})

	// /metrics is attached after construction via Server.AttachMetricsHandler,
	// because the handler is owned by the metrics provider (built in serve.go
	// before this server). Tests that don't use metrics skip the attachment.

	// Serve UI if enabled
	var handler http.Handler = mux
	if config.EnableUI {
		uiFS, err := httpInterface.GetUIFileSystem()
		if err == nil {
			// Create a wrapper handler that routes between UI and API
			handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// API + operator-facing endpoints go to the mux; everything else
				// falls through to the SPA so client-side routes resolve.
				p := r.URL.Path
				if strings.HasPrefix(p, "/nis.v1") ||
					p == "/livez" || p == "/healthz" || p == "/readyz" || p == "/metrics" {
					mux.ServeHTTP(w, r)
					return
				}

				// Otherwise serve the UI
				httpInterface.NewSPAHandler(uiFS).ServeHTTP(w, r)
			})
			logging.GetLogger().Info("UI enabled and will be served at /")
		} else {
			logging.GetLogger().Warn("failed to load UI filesystem", "error", err)
		}
	}

	// Metrics middleware sits *before* the tracing middleware so probe paths
	// excluded by both don't produce empty spans.
	if config.MetricsProvider != nil {
		handler = config.MetricsProvider.Recorder.HTTPMiddleware(handler)
	}

	// otelhttp wraps the handler in spans named after the HTTP route. It is a
	// no-op when no tracer provider has been set globally. We exclude probe
	// and scrape paths to keep traces clean.
	handler = otelhttp.NewHandler(handler, "nis-http",
		otelhttp.WithFilter(func(r *http.Request) bool {
			switch r.URL.Path {
			case "/metrics", "/healthz", "/livez", "/readyz":
				return false
			}
			return true
		}),
	)

	// Wrap handler with request logging middleware
	handler = logging.RequestLoggingMiddleware(handler)

	// gRPC server reflection. Lets tools (grpcurl, Postman, Bruno, Kreya)
	// discover services and message schemas without a local .proto copy.
	// Reflection uses a bidi-streaming RPC that needs http.Flusher on the
	// response writer; otelhttp / metrics / logging middlewares wrap the writer
	// in ways that don't preserve Flusher, so reflection is registered on an
	// outer dispatcher that runs before those wrappers. Unauthenticated by
	// design — reflection exposes schema only; the underlying RPCs remain
	// auth-gated by the interceptor above.
	reflector := grpcreflect.NewStaticReflector(
		nisv1connect.OperatorServiceName,
		nisv1connect.AccountServiceName,
		nisv1connect.UserServiceName,
		nisv1connect.ScopedSigningKeyServiceName,
		nisv1connect.ClusterServiceName,
		nisv1connect.AuthServiceName,
		nisv1connect.ExportServiceName,
		nisv1connect.EventServiceName,
		nisv1connect.WebhookServiceName,
		nisv1connect.APITokenServiceName,
		nisv1connect.SearchServiceName,
		nisv1connect.TemplateServiceName,
		nisv1connect.JobServiceName,
		nisv1connect.BackupServiceName,
	)
	reflectV1Path, reflectV1Handler := grpcreflect.NewHandlerV1(reflector)
	reflectV1AlphaPath, reflectV1AlphaHandler := grpcreflect.NewHandlerV1Alpha(reflector)
	instrumented := handler
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, reflectV1Path):
			reflectV1Handler.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, reflectV1AlphaPath):
			reflectV1AlphaHandler.ServeHTTP(w, r)
		default:
			instrumented.ServeHTTP(w, r)
		}
	})

	// Create HTTP/2 server with h2c (HTTP/2 without TLS) support
	// This allows both HTTP/1.1 and HTTP/2 connections
	httpServer := &http.Server{
		Addr:    config.Address,
		Handler: h2c.NewHandler(handler, &http2.Server{}),
	}

	return &Server{
		config:     config,
		httpServer: httpServer,
		mux:        mux,
	}
}

// AttachMetricsHandler registers the /metrics endpoint on the underlying mux.
// Kept separate from NewServer so the caller can pass in the actual http.Handler
// (provider returns one) without forcing every test path to wire it up.
func (s *Server) AttachMetricsHandler(h http.Handler) {
	s.mux.Handle("/metrics", h)
}

// readyzResponse is the JSON body returned by /readyz.
type readyzResponse struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

func handleReadyz(w http.ResponseWriter, r *http.Request, cfg ServerConfig) {
	resp := readyzResponse{Status: "ok", Components: map[string]string{}}
	allOK := true

	if !cfg.MigrationsDone {
		resp.Components["migrations"] = "pending"
		allOK = false
	} else {
		resp.Components["migrations"] = "ok"
	}

	if cfg.RepoFactory != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := cfg.RepoFactory.Ping(ctx); err != nil {
			resp.Components["database"] = "error: " + err.Error()
			allOK = false
		} else {
			resp.Components["database"] = "ok"
		}
	} else {
		resp.Components["database"] = "unknown: no repo factory"
		allOK = false
	}

	if cfg.Encryptor != nil {
		ct, err := cfg.Encryptor.Encrypt(r.Context(), []byte("readyz"))
		if err != nil {
			resp.Components["encryption"] = "error: " + err.Error()
			allOK = false
		} else if pt, err := cfg.Encryptor.Decrypt(r.Context(), ct); err != nil || string(pt) != "readyz" {
			resp.Components["encryption"] = "error: roundtrip failed"
			allOK = false
		} else {
			resp.Components["encryption"] = "ok"
		}
	} else {
		resp.Components["encryption"] = "unknown: no encryptor"
		allOK = false
	}

	w.Header().Set("Content-Type", "application/json")
	if allOK {
		w.WriteHeader(http.StatusOK)
	} else {
		resp.Status = "unavailable"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Start starts the gRPC server
func (s *Server) Start() error {
	logging.GetLogger().Info("starting gRPC server", "address", s.config.Address)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the gRPC server
func (s *Server) Shutdown() error {
	logging.GetLogger().Info("shutting down gRPC server")
	return s.httpServer.Close()
}
