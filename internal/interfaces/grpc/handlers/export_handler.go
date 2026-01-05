package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// ExportHandler implements the ExportService gRPC service
type ExportHandler struct {
	service *services.ExportService
}

// NewExportHandler creates a new ExportHandler
func NewExportHandler(service *services.ExportService) nisv1connect.ExportServiceHandler {
	return &ExportHandler{service: service}
}

// ExportOperator exports an operator and all its data
func (h *ExportHandler) ExportOperator(
	ctx context.Context,
	req *connect.Request[pb.ExportOperatorRequest],
) (*connect.Response[pb.ExportOperatorResponse], error) {
	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	data, err := h.service.ExportOperatorJSON(ctx, operatorID, req.Msg.IncludeSecrets)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ExportOperatorResponse{
		Data: data,
	}), nil
}

// ImportOperator imports an operator from exported data
func (h *ExportHandler) ImportOperator(
	ctx context.Context,
	req *connect.Request[pb.ImportOperatorRequest],
) (*connect.Response[pb.ImportOperatorResponse], error) {
	// Parse the data to get the operator name before importing
	var exported services.ExportedOperator
	if err := json.Unmarshal(req.Msg.Data, &exported); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("failed to parse export data: %w", err))
	}

	// Import the operator
	if err := h.service.ImportOperatorJSON(ctx, req.Msg.Data, req.Msg.RegenerateIds); err != nil {
		return nil, err
	}

	// Return the original operator ID (note: if regenerate_ids was true, a new ID was created)
	// The client will need to look up the operator by name if they need the new ID
	return connect.NewResponse(&pb.ImportOperatorResponse{
		OperatorId: mappers.UUIDToString(exported.Operator.ID),
	}), nil
}

// ImportFromNSC imports an operator from NSC archive
func (h *ExportHandler) ImportFromNSC(
	ctx context.Context,
	req *connect.Request[pb.ImportFromNSCRequest],
) (*connect.Response[pb.ImportFromNSCResponse], error) {
	operatorID, err := h.service.ImportFromNSC(ctx, req.Msg.Data, req.Msg.OperatorName)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ImportFromNSCResponse{
		OperatorId: mappers.UUIDToString(operatorID),
	}), nil
}
