package grpc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
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
	permService *services.PermissionService,
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
	operatorHandler := handlers.NewOperatorHandler(operatorService, permService)
	mux.Handle(nisv1connect.NewOperatorServiceHandler(operatorHandler, interceptorOption))

	accountHandler := handlers.NewAccountHandler(accountService, permService)
	mux.Handle(nisv1connect.NewAccountServiceHandler(accountHandler, interceptorOption))

	userHandler := handlers.NewUserHandler(userService, permService)
	mux.Handle(nisv1connect.NewUserServiceHandler(userHandler, interceptorOption))

	scopedKeyHandler := handlers.NewScopedSigningKeyHandler(scopedKeyService, permService)
	mux.Handle(nisv1connect.NewScopedSigningKeyServiceHandler(scopedKeyHandler, interceptorOption))

	clusterHandler := handlers.NewClusterHandler(clusterService, permService)
	mux.Handle(nisv1connect.NewClusterServiceHandler(clusterHandler, interceptorOption))

	authHandler := handlers.NewAuthHandler(authService)
	mux.Handle(nisv1connect.NewAuthServiceHandler(authHandler, interceptorOption))

	exportHandler := handlers.NewExportHandler(exportService, permService)
	mux.Handle(nisv1connect.NewExportServiceHandler(exportHandler, interceptorOption))

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
				// If the path starts with /nis.v1, it's an API call
				if strings.HasPrefix(r.URL.Path, "/nis.v1") {
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
