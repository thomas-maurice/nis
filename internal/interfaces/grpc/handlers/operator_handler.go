package handlers

import (
	"context"
	"time"

	"connectrpc.com/connect"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/services"
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
	operator, err := h.service.CreateOperator(ctx, services.CreateOperatorRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
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

	operator, err := h.service.GetOperatorByName(ctx, req.Msg.Name)
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

// ListOperators lists all operators
func (h *OperatorHandler) ListOperators(
	ctx context.Context,
	req *connect.Request[pb.ListOperatorsRequest],
) (*connect.Response[pb.ListOperatorsResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	operators, err := h.service.ListOperators(ctx, mappers.ProtoToListOptions(req.Msg.Options))
	if err != nil {
		return nil, err
	}

	// Filter operators based on user permissions
	filtered, err := h.permService.FilterOperators(ctx, requestingUser, operators)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&pb.ListOperatorsResponse{
		Operators: mappers.OperatorsToProto(filtered),
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
