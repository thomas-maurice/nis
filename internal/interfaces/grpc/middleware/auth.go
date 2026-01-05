package middleware

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"github.com/casbin/casbin/v2"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// AuthInterceptor provides authentication and authorization middleware
type AuthInterceptor struct {
	authService *services.AuthService
	enforcer    *casbin.Enforcer
	// Public methods that don't require authentication
	publicMethods map[string]bool
}

// NewAuthInterceptor creates a new authentication interceptor
func NewAuthInterceptor(
	authService *services.AuthService,
	enforcer *casbin.Enforcer,
) *AuthInterceptor {
	// Define public methods that don't require authentication
	publicMethods := map[string]bool{
		"/nis.v1.AuthService/Login": true,
	}

	return &AuthInterceptor{
		authService:   authService,
		enforcer:      enforcer,
		publicMethods: publicMethods,
	}
}

// contextKey is the type for context keys to avoid collisions
type contextKey string

const (
	// UserContextKey is the context key for the authenticated user
	UserContextKey contextKey = "user"
)

// WrapUnary wraps a unary RPC with authentication
func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Check if this is a public method
		if i.publicMethods[req.Spec().Procedure] {
			return next(ctx, req)
		}

		// Extract token from Authorization header
		token := extractToken(req.Header().Get("Authorization"))
		if token == "" {
			return nil, connect.NewError(connect.CodeUnauthenticated, nil)
		}

		// Validate token and get user
		user, err := i.authService.ValidateToken(ctx, token)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}

		// Check authorization with Casbin
		resource, action := extractResourceAndAction(req.Spec().Procedure)
		allowed, err := i.enforcer.Enforce(string(user.Role), resource, action)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if !allowed {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}

		// Add user to context
		ctx = context.WithValue(ctx, UserContextKey, user)

		return next(ctx, req)
	}
}

// WrapStreamingClient wraps a streaming client RPC with authentication
func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler wraps a streaming handler RPC with authentication
func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Check if this is a public method
		if i.publicMethods[conn.Spec().Procedure] {
			return next(ctx, conn)
		}

		// Extract token from Authorization header
		token := extractToken(conn.RequestHeader().Get("Authorization"))
		if token == "" {
			return connect.NewError(connect.CodeUnauthenticated, nil)
		}

		// Validate token and get user
		user, err := i.authService.ValidateToken(ctx, token)
		if err != nil {
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		// Check authorization with Casbin
		resource, action := extractResourceAndAction(conn.Spec().Procedure)
		allowed, err := i.enforcer.Enforce(string(user.Role), resource, action)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		if !allowed {
			return connect.NewError(connect.CodePermissionDenied, nil)
		}

		// Add user to context
		ctx = context.WithValue(ctx, UserContextKey, user)

		return next(ctx, conn)
	}
}

// extractToken extracts the bearer token from the Authorization header
func extractToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}

	// Expected format: "Bearer <token>"
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}

	return parts[1]
}

// extractResourceAndAction extracts the resource and action from a procedure name
// Example: "/nis.v1.OperatorService/CreateOperator" -> ("operator", "create")
func extractResourceAndAction(procedure string) (string, string) {
	// Split by "/"
	parts := strings.Split(procedure, "/")
	if len(parts) < 3 {
		return "", ""
	}

	// Get the method name (last part)
	method := parts[len(parts)-1]

	// Get the service name (second to last part)
	service := parts[len(parts)-2]

	// Extract resource from service name
	// Example: "nis.v1.OperatorService" -> "operator"
	serviceParts := strings.Split(service, ".")
	serviceName := serviceParts[len(serviceParts)-1]
	resource := strings.ToLower(strings.TrimSuffix(serviceName, "Service"))

	// Extract action from method name
	// Example: "CreateOperator" -> "create"
	action := extractAction(method)

	return resource, action
}

// extractAction extracts the action from a method name
func extractAction(method string) string {
	method = strings.ToLower(method)

	if strings.HasPrefix(method, "create") {
		return "create"
	}
	if strings.HasPrefix(method, "update") {
		return "update"
	}
	if strings.HasPrefix(method, "delete") {
		return "delete"
	}
	if strings.HasPrefix(method, "get") || strings.HasPrefix(method, "list") {
		return "read"
	}

	// Default to read for unknown methods
	return "read"
}

// GetUserFromContext retrieves the authenticated user from context
func GetUserFromContext(ctx context.Context) (*entities.APIUser, bool) {
	user, ok := ctx.Value(UserContextKey).(*entities.APIUser)
	return user, ok
}
