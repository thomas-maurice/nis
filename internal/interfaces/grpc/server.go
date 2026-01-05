package grpc

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/handlers"
	httpInterface "github.com/thomas-maurice/nis/internal/interfaces/http"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// ServerConfig contains configuration for the gRPC server
type ServerConfig struct {
	Address  string
	EnableUI bool
}

// Server wraps the HTTP server for gRPC/ConnectRPC
type Server struct {
	config      ServerConfig
	httpServer  *http.Server
	mux         *http.ServeMux
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
) *Server {
	mux := http.NewServeMux()

	// Register all service handlers
	operatorHandler := handlers.NewOperatorHandler(operatorService)
	mux.Handle(nisv1connect.NewOperatorServiceHandler(operatorHandler))

	accountHandler := handlers.NewAccountHandler(accountService)
	mux.Handle(nisv1connect.NewAccountServiceHandler(accountHandler))

	userHandler := handlers.NewUserHandler(userService)
	mux.Handle(nisv1connect.NewUserServiceHandler(userHandler))

	scopedKeyHandler := handlers.NewScopedSigningKeyHandler(scopedKeyService)
	mux.Handle(nisv1connect.NewScopedSigningKeyServiceHandler(scopedKeyHandler))

	clusterHandler := handlers.NewClusterHandler(clusterService)
	mux.Handle(nisv1connect.NewClusterServiceHandler(clusterHandler))

	authHandler := handlers.NewAuthHandler(authService)
	mux.Handle(nisv1connect.NewAuthServiceHandler(authHandler))

	exportHandler := handlers.NewExportHandler(exportService)
	mux.Handle(nisv1connect.NewExportServiceHandler(exportHandler))

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
			fmt.Println("UI enabled and will be served at /")
		} else {
			fmt.Printf("Warning: Failed to load UI filesystem: %v\n", err)
		}
	}

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

// Start starts the gRPC server
func (s *Server) Start() error {
	fmt.Printf("Starting gRPC server on %s\n", s.config.Address)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the gRPC server
func (s *Server) Shutdown() error {
	fmt.Println("Shutting down gRPC server...")
	return s.httpServer.Close()
}
