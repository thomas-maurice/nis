package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/authctx"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
)

// AuthInterceptor provides authentication and authorization middleware. It is
// the single entry point for both. Per-row checks live downstream in the
// handlers via PermissionService.Can*; the gate enforced here is the coarse
// (role, resource, action) authority from authz.RolePolicy.
//
// Routing of an incoming procedure path to (resource, action, kind) is done
// via authz.ResolveProcedure — see internal/application/authz/registry.go for
// the single source of truth. The pre-A17 verb-prefix heuristic
// (extractAction) and the Casbin enforcer are gone; both are replaced by
// direct lookups against that registry.
type AuthInterceptor struct {
	authService     *services.AuthService
	apiTokenService *services.APITokenService
	tokenFlusher    *APITokenLastUsedFlusher
}

// NewAuthInterceptor creates a new authentication interceptor.
//
// apiTokenService is optional — if nil, the token-auth fork is disabled and
// every request is treated as a JWT-bearing user request. Production wiring
// always provides it; tests that exercise only the JWT path can leave it nil.
//
// tokenFlusher batches last_used_at updates from the hot path. If nil, the
// last_used_at column simply does not get refreshed (used by tests).
func NewAuthInterceptor(authService *services.AuthService) *AuthInterceptor {
	return &AuthInterceptor{
		authService: authService,
	}
}

// WithAPITokenService wires the APITokenService and (optional) last-used flusher
// into the interceptor, enabling the `nis_pat_` token auth path. Returns the
// receiver for fluent setup at construction time.
func (i *AuthInterceptor) WithAPITokenService(svc *services.APITokenService, flusher *APITokenLastUsedFlusher) *AuthInterceptor {
	i.apiTokenService = svc
	i.tokenFlusher = flusher
	return i
}

// contextKey aliases authctx.ContextKey so tests in this package can use the
// unexported name (contextKey) to construct test keys without importing authctx.
type contextKey = authctx.ContextKey

// UserContextKey re-exports authctx.UserContextKey so existing callers that
// reference middleware.UserContextKey continue to work.
var UserContextKey = authctx.UserContextKey

// authenticate runs the auth + authz checks shared by unary and streaming
// handlers. On success it returns the new context (with user and possibly
// actor set); on failure it returns an already-formatted Connect error. The
// caller can ignore the returned ctx when err != nil.
func (i *AuthInterceptor) authenticate(ctx context.Context, procedure, authHeader string) (context.Context, error) {
	proc, known := authz.ResolveProcedure(procedure)

	// Unknown procedure — default deny. The lint test refuses to ship a
	// handler without a matching registry entry, so reaching this branch at
	// runtime indicates an unsynced gen / hand-edit / proto rename.
	if !known {
		metrics.Default().RecordAuthRejection(ctx, "unknown_procedure")
		return ctx, connect.NewError(connect.CodePermissionDenied, nil)
	}

	// Public procedures (Login, ValidateToken) skip every check.
	if proc.Kind == authz.KindPublic {
		return ctx, nil
	}

	token := extractToken(authHeader)
	if token == "" {
		metrics.Default().RecordAuthRejection(ctx, "missing_token")
		return ctx, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	user, actor, err := i.resolveCaller(ctx, token)
	if err != nil {
		return ctx, err
	}

	if !authz.RolePermits(user.Role, proc.Resource, proc.Action) {
		metrics.Default().RecordAuthRejection(ctx, "forbidden")
		return ctx, connect.NewError(connect.CodePermissionDenied, nil)
	}

	ctx = authctx.SetUser(ctx, user)
	if actor != nil {
		ctx = authctx.SetActor(ctx, *actor)
	}
	return ctx, nil
}

// resolveCaller decides whether the bearer credential is a service-account API
// token (prefix nis_pat_) or a JWT, and returns the synthetic APIUser plus an
// optional Actor for audit attribution. Errors are already Connect-typed.
func (i *AuthInterceptor) resolveCaller(ctx context.Context, token string) (*entities.APIUser, *authctx.Actor, error) {
	if i.apiTokenService != nil && strings.HasPrefix(token, entities.APITokenPrefix) {
		apiToken, err := i.apiTokenService.Authenticate(ctx, token)
		if err != nil {
			i.recordTokenAuthFailure(ctx, err)
			return nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		metrics.Default().RecordAPITokenAuthentication(ctx, "success")
		metrics.Default().RecordAuthRejection(ctx, "") // no-op; success path does not increment rejections

		// Defer last_used_at to the coalescing flusher so we don't write per request.
		if i.tokenFlusher != nil {
			i.tokenFlusher.Touch(apiToken.ID, time.Now().UTC())
		}

		synthetic := i.apiTokenService.SyntheticAPIUser(apiToken)
		tokenID := apiToken.ID
		return synthetic, &authctx.Actor{Type: entities.ActorTypeAPIToken, ID: &tokenID}, nil
	}

	user, err := i.authService.ValidateToken(ctx, token)
	if err != nil {
		metrics.Default().RecordAuthRejection(ctx, "invalid_token")
		return nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return user, nil, nil
}

func (i *AuthInterceptor) recordTokenAuthFailure(ctx context.Context, err error) {
	switch {
	case errors.Is(err, services.ErrAPITokenExpired):
		metrics.Default().RecordAPITokenAuthentication(ctx, "expired")
		metrics.Default().RecordAuthRejection(ctx, "invalid_api_token")
	case errors.Is(err, services.ErrAPITokenRevoked):
		metrics.Default().RecordAPITokenAuthentication(ctx, "revoked")
		metrics.Default().RecordAuthRejection(ctx, "invalid_api_token")
	default:
		metrics.Default().RecordAPITokenAuthentication(ctx, "invalid")
		metrics.Default().RecordAuthRejection(ctx, "invalid_api_token")
	}
}

// WrapUnary wraps a unary RPC with authentication
func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		newCtx, err := i.authenticate(ctx, req.Spec().Procedure, req.Header().Get("Authorization"))
		if err != nil {
			return nil, err
		}
		return next(newCtx, req)
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
		newCtx, err := i.authenticate(ctx, conn.Spec().Procedure, conn.RequestHeader().Get("Authorization"))
		if err != nil {
			return err
		}
		return next(newCtx, conn)
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

// GetUserFromContext retrieves the authenticated user from context.
// Deprecated: use authctx.GetUser directly. Kept for callers outside this package.
func GetUserFromContext(ctx context.Context) (*entities.APIUser, bool) {
	return authctx.GetUser(ctx)
}

// TokenIDFromContext returns the api_token ID set by the middleware when a request
// was authenticated with a service-account token. Returns (uuid.Nil, false) for
// user-authed requests.
func TokenIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	actor, ok := authctx.GetActor(ctx)
	if !ok || actor.Type != entities.ActorTypeAPIToken || actor.ID == nil {
		return uuid.Nil, false
	}
	return *actor.ID, true
}
