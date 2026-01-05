package handlers

import (
	"context"

	"connectrpc.com/connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// OperatorHandler implements the OperatorService gRPC service
type OperatorHandler struct {
	service *services.OperatorService
}

// NewOperatorHandler creates a new OperatorHandler
func NewOperatorHandler(service *services.OperatorService) nisv1connect.OperatorServiceHandler {
	return &OperatorHandler{service: service}
}

// CreateOperator creates a new operator
func (h *OperatorHandler) CreateOperator(
	ctx context.Context,
	req *connect.Request[pb.CreateOperatorRequest],
) (*connect.Response[pb.CreateOperatorResponse], error) {
	operator, err := h.service.CreateOperator(ctx, services.CreateOperatorRequest{
		Name:                req.Msg.Name,
		Description:         req.Msg.Description,
		SystemAccountPubKey: req.Msg.SystemAccountPubKey,
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
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	operator, err := h.service.GetOperator(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
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
	operator, err := h.service.GetOperatorByName(ctx, req.Msg.Name)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
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
	operators, err := h.service.ListOperators(ctx, mappers.ProtoToListOptions(req.Msg.Options))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ListOperatorsResponse{
		Operators: mappers.OperatorsToProto(operators),
	}), nil
}

// UpdateOperator updates an operator
func (h *OperatorHandler) UpdateOperator(
	ctx context.Context,
	req *connect.Request[pb.UpdateOperatorRequest],
) (*connect.Response[pb.UpdateOperatorResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	operator, err := h.service.UpdateOperator(ctx, id, services.UpdateOperatorRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
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
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	operator, err := h.service.SetSystemAccount(ctx, id, req.Msg.SystemAccountPubKey)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
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
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	err = h.service.DeleteOperator(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.DeleteOperatorResponse{}), nil
}

// GenerateInclude generates NATS server configuration for an operator
func (h *OperatorHandler) GenerateInclude(
	ctx context.Context,
	req *connect.Request[pb.GenerateIncludeRequest],
) (*connect.Response[pb.GenerateIncludeResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	config, err := h.service.GenerateInclude(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GenerateIncludeResponse{
		Config: config,
	}), nil
}
