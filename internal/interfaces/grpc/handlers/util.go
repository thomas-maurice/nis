package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

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

// resolveEffectiveOrg computes the organization a per-org operation targets,
// for handlers where operator names (and thus name lookups / creates) are
// scoped per-org rather than globally.
//
//   - Org-scoped callers (any non-admin role — OrganizationID is set) are
//     pinned to their own org. The request's organization_id field is ignored
//     entirely: a token cannot reach across orgs by lying about the field.
//   - Platform admins (RoleAdmin, OrganizationID nil) carry no org binding, so
//     they MUST supply organization_id; an empty field is an InvalidArgument
//     rather than a silent fall-through to the default org.
func resolveEffectiveOrg(user *entities.APIUser, requestedOrgID string) (uuid.UUID, error) {
	if user.OrganizationID != nil {
		return *user.OrganizationID, nil
	}
	if requestedOrgID == "" {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("organization_id is required: platform admins must specify the target organization"))
	}
	id, err := uuid.Parse(requestedOrgID)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("organization_id: "+err.Error()))
	}
	return id, nil
}

// requireAdmin is the defense-in-depth admin gate for KindRoleOnly handlers
// (jobs / events / config / scheduled-backup imports / operator create). The
// authz registry's RolePolicy is the primary gate — these handlers all have
// a resource×action grant restricted to admin only. requireAdmin pins that
// expectation in code so a future RolePolicy edit that accidentally widens
// access (e.g. adds `operator-admin.job.read`) doesn't silently expose the
// handler.
//
// Per A19 (2026-05-23): every KindRoleOnly handler invokes this helper. The
// lint test `handler_authz_lint_test.go` enforces the convention going
// forward — a KindRoleOnly classification without a requireAdmin call fails
// the build.
//
// Use only when:
//   - the registry row for the procedure is admin-only;
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
