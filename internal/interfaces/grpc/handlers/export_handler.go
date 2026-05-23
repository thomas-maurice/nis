package handlers

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// ExportHandler implements the ExportService gRPC service
type ExportHandler struct {
	service     *services.ExportService
	permService *services.PermissionService
}

// NewExportHandler creates a new ExportHandler
func NewExportHandler(service *services.ExportService, permService *services.PermissionService) nisv1connect.ExportServiceHandler {
	return &ExportHandler{
		service:     service,
		permService: permService,
	}
}

// ExportOperator exports an operator and all its data
func (h *ExportHandler) ExportOperator(
	ctx context.Context,
	req *connect.Request[pb.ExportOperatorRequest],
) (*connect.Response[pb.ExportOperatorResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this operator
	if err := h.permService.CanReadOperator(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// Empty format → JSON, preserving pre-yaml behaviour for older clients.
	format := services.ExportFormat(req.Msg.Format)
	if format == "" {
		format = services.FormatJSON
	}

	// Every export carries seeds; the only knob is the form. Default = encrypted.
	mode := services.SecretsEncrypted
	if req.Msg.PlaintextSecrets {
		mode = services.SecretsPlaintext
	}

	data, err := h.service.ExportOperatorBytes(ctx, operatorID, mode, format)
	if err != nil {
		// Unsupported format becomes InvalidArgument; everything else stays as-is.
		if format != services.FormatJSON && format != services.FormatYAML {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.ExportOperatorResponse{
		Data:   data,
		Format: string(format),
	}), nil
}

// ImportOperator imports an operator from exported data. Admin-only via the
// shared requireAdmin gate (util.go); Casbin's policy row mirrors that.
func (h *ExportHandler) ImportOperator(
	ctx context.Context,
	req *connect.Request[pb.ImportOperatorRequest],
) (*connect.Response[pb.ImportOperatorResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	// Parse first (auto-detects JSON or YAML) so we can echo the operator ID
	// back in the response.
	exported, err := services.ParseExport(req.Msg.Data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Import the operator. ImportOperatorBytes re-parses internally; the cost
	// is negligible vs. the dozens of writes that follow. Overwrite=true
	// turns the call into a subtree replacement when the operator ID already
	// exists (preserves clusters); false refuses on existing-ID with
	// FailedPrecondition. See ImportOperator doc for full semantics.
	if err := h.service.ImportOperatorBytes(ctx, req.Msg.Data, req.Msg.Overwrite); err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.ImportOperatorResponse{
		OperatorId: mappers.UUIDToString(exported.Operator.ID),
	}), nil
}

// ImportFromNSC imports an operator from an NSC archive. Admin-only via the
// shared requireAdmin gate (util.go); Casbin's policy row mirrors that.
func (h *ExportHandler) ImportFromNSC(
	ctx context.Context,
	req *connect.Request[pb.ImportFromNSCRequest],
) (*connect.Response[pb.ImportFromNSCResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	operatorID, err := h.service.ImportFromNSC(ctx, req.Msg.Data, req.Msg.OperatorName)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ImportFromNSCResponse{
		OperatorId: mappers.UUIDToString(operatorID),
	}), nil
}
