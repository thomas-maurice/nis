package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
)

// repoErrToConnect translates well-known repository sentinel errors into the
// matching Connect-RPC status codes, leaving everything else untouched (the
// Connect framework will surface those as `Unknown`/internal). It centralises
// what used to be a 5-line `if errors.Is(...) { return CodeX } return CodeY` block
// at ~40 handler sites.
func repoErrToConnect(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repositories.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, repositories.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, services.ErrOperatorHasClusters),
		errors.Is(err, services.ErrOperatorImportExists),
		errors.Is(err, services.ErrOperatorImportNameConflict),
		errors.Is(err, services.ErrTemplateHasDependents),
		errors.Is(err, services.ErrScopedKeyNotTemplated),
		errors.Is(err, services.ErrTemplateRefForeignOperator),
		errors.Is(err, services.ErrTemplateVersionNotFound),
		errors.Is(err, services.ErrSSKTrackLatestRequiresTemplate),
		errors.Is(err, services.ErrSSKTrackingLatest),
		errors.Is(err, services.ErrSSKTrackLatestDrifted):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return err
	}
}

// errSweeperNotConfigured is returned by RunJWTExpirySweep when the server was
// built without a sweeper wired in (test paths only).
var errSweeperNotConfigured = errors.New("jwt expiry sweeper is not configured on this server")

// mapJobStateErr translates JobRepository sentinels into Connect codes.
// ErrJobInvalidStateTransition is FailedPrecondition (admin asked us to
// retry/cancel a row in a state that doesn't allow it); ErrNotFound is
// NotFound; anything else falls through.
func mapJobStateErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repositories.ErrJobInvalidStateTransition):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, repositories.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return err
	}
}

// authedUser returns the API user attached to the request by the auth interceptor.
// If the context has no user (request never passed through auth), the caller gets
// an `Unauthenticated` error suitable for returning directly from a handler.
func authedUser(ctx context.Context) (*entities.APIUser, error) {
	user, ok := middleware.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return user, nil
}

// requireAdmin is the defense-in-depth admin gate for casbinOnly handlers
// (jobs / events / config / scheduled-backup imports / operator create).
// Casbin's policy file is the primary gate — these handlers all have a
// resource×action row restricted to admin only. requireAdmin pins that
// expectation in code so a future Casbin-policy edit that accidentally
// widens access (e.g. adds `operator-admin.job.read`) doesn't silently
// expose the handler.
//
// Per A19 (2026-05-23): every casbinOnly handler invokes this helper. The
// lint test `handler_authz_lint_test.go` enforces the convention going
// forward — a casbinOnly classification without a requireAdmin call fails
// the build.
//
// Use only when:
//   - the Casbin policy row for the procedure is admin-only;
//   - there is no per-row narrowing to enforce (jobs are infrastructure,
//     not tenant data; events are global audit; config is global runtime).
// For per-tenant narrowing use `permService.Can*` from the handler instead.
func requireAdmin(ctx context.Context) error {
	user, err := authedUser(ctx)
	if err != nil {
		return err
	}
	if user.Role != entities.RoleAdmin {
		return connect.NewError(connect.CodePermissionDenied, nil)
	}
	return nil
}
