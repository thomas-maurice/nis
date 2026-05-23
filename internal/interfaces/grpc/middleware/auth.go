package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/casbin/casbin/v2"
	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/authctx"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
)

// AuthInterceptor provides authentication and authorization middleware
type AuthInterceptor struct {
	authService     *services.AuthService
	apiTokenService *services.APITokenService
	tokenFlusher    *APITokenLastUsedFlusher
	enforcer        *casbin.Enforcer
	// Public methods that don't require authentication
	publicMethods map[string]bool
}

// NewAuthInterceptor creates a new authentication interceptor.
//
// apiTokenService is optional — if nil, the token-auth fork is disabled and
// every request is treated as a JWT-bearing user request. Production wiring
// always provides it; tests that exercise only the JWT path can leave it nil.
//
// tokenFlusher batches last_used_at updates from the hot path. If nil, the
// last_used_at column simply does not get refreshed (used by tests).
func NewAuthInterceptor(
	authService *services.AuthService,
	enforcer *casbin.Enforcer,
) *AuthInterceptor {
	publicMethods := map[string]bool{
		"/nis.v1.AuthService/Login": true,
	}
	return &AuthInterceptor{
		authService:   authService,
		enforcer:      enforcer,
		publicMethods: publicMethods,
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
	if i.publicMethods[procedure] {
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

	resource, action := extractResourceAndAction(procedure)
	allowed, err := i.enforcer.Enforce(string(user.Role), resource, action)
	if err != nil {
		return ctx, connect.NewError(connect.CodeInternal, err)
	}
	if !allowed {
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

	// Special case mappings for resource names
	if resource == "auth" && strings.Contains(strings.ToLower(method), "apiuser") {
		resource = "api_user"
	}
	if resource == "scopedsigningkey" {
		resource = "scoped_key"
	}

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
	if strings.HasPrefix(method, "delete") || strings.HasPrefix(method, "revoke") || strings.HasPrefix(method, "cancel") {
		// Revoking, deleting, and cancelling a pending job are different DB
		// writes but the same authority: you cannot revoke a token / cancel
		// a job without the ability to remove it.
		return "delete"
	}
	if strings.HasPrefix(method, "apply") || strings.HasPrefix(method, "detach") || strings.HasPrefix(method, "bump") || strings.HasPrefix(method, "settracklatest") || strings.HasPrefix(method, "retry") || strings.HasPrefix(method, "run") || strings.HasPrefix(method, "rotate") || strings.HasPrefix(method, "push") {
		// P6 template ops: applying a template version, bumping an SSK to
		// a new version, detaching from a template, or toggling
		// track_latest all mutate the SSK's binding/perm columns and
		// (for bump/apply) the parent account JWT. Same authority as a
		// plain "update". Without this, the default would fall through
		// to "read" and silently bypass Casbin's mutation rows.
		//
		// P12 RunOperatorBackup → "update": triggering a backup mints an
		// S3 object + DB row; gating it on Casbin's `backup.update` rather
		// than `read` matches its actual blast radius.
		//
		// P3 RotateScopedSigningKey → "update": rotation replaces an SSK's
		// key material + re-mints every dependent user JWT + adds revocations
		// to the parent account JWT. Without this prefix, "rotate*" falls
		// through to "read" and every role that can read the SSK could
		// rotate it — silent privilege escalation.
		//
		// A5 PushAccountJWT → "update": pushing rewrites the resolver-side
		// account JWT. Same blast radius as a plain account update. Without
		// this prefix, "push*" falls through to "read" — every role that can
		// read the account could trigger a resolver overwrite.
		return "update"
	}
	if strings.HasPrefix(method, "get") || strings.HasPrefix(method, "list") {
		return "read"
	}

	// Default to read for unknown methods
	return "read"
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
