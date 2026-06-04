package handlers

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// OperatorHandler implements the OperatorService gRPC service
type OperatorHandler struct {
	service     *services.OperatorService
	permService *services.PermissionService
	sweeper     *services.JWTExpirySweeper
}

// NewOperatorHandler creates a new OperatorHandler. sweeper may be nil for
// tests that don't exercise RunJWTExpirySweep.
func NewOperatorHandler(service *services.OperatorService, permService *services.PermissionService, sweeper *services.JWTExpirySweeper) nisv1connect.OperatorServiceHandler {
	return &OperatorHandler{
		service:     service,
		permService: permService,
		sweeper:     sweeper,
	}
}

// CreateOperator creates a new operator
func (h *OperatorHandler) CreateOperator(
	ctx context.Context,
	req *connect.Request[pb.CreateOperatorRequest],
) (*connect.Response[pb.CreateOperatorResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.permService.CanCreateOperator(requestingUser); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	// Operator names are unique per-org. Org-scoped callers land in their own
	// org; platform admins must name the target org explicitly.
	orgID, err := resolveEffectiveOrg(requestingUser, req.Msg.GetOrganizationId())
	if err != nil {
		return nil, err
	}
	operator, err := h.service.CreateOperator(ctx, services.CreateOperatorRequest{
		Name:           req.Msg.Name,
		Description:    req.Msg.Description,
		OrganizationID: &orgID,
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CreateOperatorResponse{
		Operator: mappers.OperatorToProto(operator),
	}), nil
}

// GetOperator retrieves an operator by ID
func (h *OperatorHandler) GetOperator(
	ctx context.Context,
	req *connect.Request[pb.GetOperatorRequest],
) (*connect.Response[pb.GetOperatorResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this operator
	if err := h.permService.CanReadOperator(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	operator, err := h.service.GetOperator(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.GetOperatorResponse{
		Operator: mappers.OperatorToProto(operator),
	}), nil
}

// GetOperatorByName retrieves an operator by name
func (h *OperatorHandler) GetOperatorByName(
	ctx context.Context,
	req *connect.Request[pb.GetOperatorByNameRequest],
) (*connect.Response[pb.GetOperatorByNameResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	// Names are unique per-org, not globally, so a name alone is ambiguous.
	// Org-scoped callers (org-admin and below carry an OrganizationID) are pinned
	// to their own org and the request's organization_id is ignored; platform
	// admins MUST supply organization_id — an empty field is InvalidArgument
	// rather than a guess across orgs.
	orgID, err := resolveEffectiveOrg(requestingUser, req.Msg.GetOrganizationId())
	if err != nil {
		return nil, err
	}
	operator, err := h.service.GetOperatorByName(ctx, orgID, req.Msg.Name)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to read this operator
	if err := h.permService.CanReadOperator(ctx, requestingUser, operator.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&pb.GetOperatorByNameResponse{
		Operator: mappers.OperatorToProto(operator),
	}), nil
}

// ListOperators lists operators with cursor pagination and SQL-level scope enforcement.
func (h *OperatorHandler) ListOperators(
	ctx context.Context,
	req *connect.Request[pb.ListOperatorsRequest],
) (*connect.Response[pb.ListOperatorsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.permService.CanListOperators(requestingUser); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	scope := authz.ScopeFromAPIUser(requestingUser)

	filter := repositories.OperatorListFilter{
		NameLike: strings.TrimSpace(req.Msg.NameLike),
	}
	if req.Msg.Page != nil {
		filter.Limit = int(req.Msg.Page.Limit)
		filter.Cursor = req.Msg.Page.Cursor
	}

	operators, nextCursor, err := h.service.ListOperatorsPage(ctx, scope, filter)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&pb.ListOperatorsResponse{
		Operators:  mappers.OperatorsToProto(operators),
		NextCursor: nextCursor,
	}), nil
}

// UpdateOperator updates an operator
func (h *OperatorHandler) UpdateOperator(
	ctx context.Context,
	req *connect.Request[pb.UpdateOperatorRequest],
) (*connect.Response[pb.UpdateOperatorResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to update this operator
	if err := h.permService.CanUpdateOperator(requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	operator, err := h.service.UpdateOperator(ctx, id, services.UpdateOperatorRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.UpdateOperatorResponse{
		Operator: mappers.OperatorToProto(operator),
	}), nil
}

// SetSystemAccount sets the system account for an operator
func (h *OperatorHandler) SetSystemAccount(
	ctx context.Context,
	req *connect.Request[pb.SetSystemAccountRequest],
) (*connect.Response[pb.SetSystemAccountResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to update this operator
	if err := h.permService.CanUpdateOperator(requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	operator, err := h.service.SetSystemAccount(ctx, id, req.Msg.SystemAccountPubKey)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.SetSystemAccountResponse{
		Operator: mappers.OperatorToProto(operator),
	}), nil
}

// DeleteOperator deletes an operator
func (h *OperatorHandler) DeleteOperator(
	ctx context.Context,
	req *connect.Request[pb.DeleteOperatorRequest],
) (*connect.Response[pb.DeleteOperatorResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to delete this operator
	if err := h.permService.CanDeleteOperator(requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	err = h.service.DeleteOperator(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.DeleteOperatorResponse{}), nil
}

// GenerateInclude generates NATS server configuration for an operator
func (h *OperatorHandler) GenerateInclude(
	ctx context.Context,
	req *connect.Request[pb.GenerateIncludeRequest],
) (*connect.Response[pb.GenerateIncludeResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this operator
	if err := h.permService.CanReadOperator(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	config, err := h.service.GenerateInclude(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.GenerateIncludeResponse{
		Config: config,
	}), nil
}

// SetJWTPolicy updates the operator's JWT lifecycle policy (P2). Admin-only.
func (h *OperatorHandler) SetJWTPolicy(
	ctx context.Context,
	req *connect.Request[pb.SetJWTPolicyRequest],
) (*connect.Response[pb.SetJWTPolicyResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permService.CanSetOperatorJWTPolicy(requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	policy := services.JWTPolicyUpdate{}
	if req.Msg.UserJwtTtlSeconds != nil {
		d := time.Duration(*req.Msg.UserJwtTtlSeconds) * time.Second
		policy.UserJWTTTL = &d
	}
	if req.Msg.AccountJwtTtlSeconds != nil {
		d := time.Duration(*req.Msg.AccountJwtTtlSeconds) * time.Second
		policy.AccountJWTTTL = &d
	}
	if req.Msg.JwtWarnWindowSeconds != nil {
		d := time.Duration(*req.Msg.JwtWarnWindowSeconds) * time.Second
		policy.JWTWarnWindow = &d
	}
	if req.Msg.JwtAutoRenew != nil {
		v := *req.Msg.JwtAutoRenew
		policy.JWTAutoRenew = &v
	}

	operator, err := h.service.SetJWTPolicy(ctx, id, policy)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.SetJWTPolicyResponse{
		Operator: mappers.OperatorToProto(operator),
	}), nil
}

// RunJWTExpirySweep forces an immediate sweep tick. Admin-only. Useful for
// tests and for ops who want to confirm an immediate response to a policy
// change.
func (h *OperatorHandler) RunJWTExpirySweep(
	ctx context.Context,
	req *connect.Request[pb.RunJWTExpirySweepRequest],
) (*connect.Response[pb.RunJWTExpirySweepResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.permService.CanRunJWTExpirySweep(requestingUser); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if h.sweeper == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSweeperNotConfigured)
	}
	res, err := h.sweeper.Tick(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.RunJWTExpirySweepResponse{
		RevocationsPruned:    int32(res.RevocationsPruned),
		ExpiringSoonEmitted:  int32(res.ExpiringSoonEmitted),
		ExpiredAlertsEmitted: int32(res.ExpiredAlertsEmitted),
		AutoRenewed:          int32(res.AutoRenewed),
	}), nil
}
