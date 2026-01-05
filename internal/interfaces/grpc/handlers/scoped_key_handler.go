package handlers

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// ScopedSigningKeyHandler implements the ScopedSigningKeyService gRPC service
type ScopedSigningKeyHandler struct {
	service *services.ScopedSigningKeyService
}

// NewScopedSigningKeyHandler creates a new ScopedSigningKeyHandler
func NewScopedSigningKeyHandler(service *services.ScopedSigningKeyService) nisv1connect.ScopedSigningKeyServiceHandler {
	return &ScopedSigningKeyHandler{service: service}
}

// CreateScopedSigningKey creates a new scoped signing key
func (h *ScopedSigningKeyHandler) CreateScopedSigningKey(
	ctx context.Context,
	req *connect.Request[pb.CreateScopedSigningKeyRequest],
) (*connect.Response[pb.CreateScopedSigningKeyResponse], error) {
	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	pubAllow, pubDeny, subAllow, subDeny := mappers.ProtoToUserPermissions(req.Msg.Permissions)
	respMaxMsgs, respExpires := mappers.ProtoToResponsePermission(req.Msg.ResponsePermission)

	key, err := h.service.CreateScopedSigningKey(ctx, services.CreateScopedSigningKeyRequest{
		AccountID:       accountID,
		Name:            req.Msg.Name,
		Description:     req.Msg.Description,
		PubAllow:        pubAllow,
		PubDeny:         pubDeny,
		SubAllow:        subAllow,
		SubDeny:         subDeny,
		ResponseMaxMsgs: respMaxMsgs,
		ResponseTTL:     time.Duration(respExpires),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CreateScopedSigningKeyResponse{
		Key: mappers.ScopedSigningKeyToProto(key),
	}), nil
}

// GetScopedSigningKey retrieves a scoped signing key by ID
func (h *ScopedSigningKeyHandler) GetScopedSigningKey(
	ctx context.Context,
	req *connect.Request[pb.GetScopedSigningKeyRequest],
) (*connect.Response[pb.GetScopedSigningKeyResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	key, err := h.service.GetScopedSigningKey(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetScopedSigningKeyResponse{
		Key: mappers.ScopedSigningKeyToProto(key),
	}), nil
}

// GetScopedSigningKeyByName retrieves a scoped signing key by name
func (h *ScopedSigningKeyHandler) GetScopedSigningKeyByName(
	ctx context.Context,
	req *connect.Request[pb.GetScopedSigningKeyByNameRequest],
) (*connect.Response[pb.GetScopedSigningKeyByNameResponse], error) {
	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	key, err := h.service.GetScopedSigningKeyByName(ctx, accountID, req.Msg.Name)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetScopedSigningKeyByNameResponse{
		Key: mappers.ScopedSigningKeyToProto(key),
	}), nil
}

// ListScopedSigningKeys lists scoped signing keys for an account
func (h *ScopedSigningKeyHandler) ListScopedSigningKeys(
	ctx context.Context,
	req *connect.Request[pb.ListScopedSigningKeysRequest],
) (*connect.Response[pb.ListScopedSigningKeysResponse], error) {
	// If account_id is empty, list all scoped signing keys
	if req.Msg.AccountId == "" {
		keys, err := h.service.ListAllScopedSigningKeys(ctx, mappers.ProtoToListOptions(req.Msg.Options))
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(&pb.ListScopedSigningKeysResponse{
			Keys: mappers.ScopedSigningKeysToProto(keys),
		}), nil
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	keys, err := h.service.ListScopedSigningKeysByAccount(ctx, accountID, mappers.ProtoToListOptions(req.Msg.Options))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ListScopedSigningKeysResponse{
		Keys: mappers.ScopedSigningKeysToProto(keys),
	}), nil
}

// UpdateScopedSigningKey updates a scoped signing key
func (h *ScopedSigningKeyHandler) UpdateScopedSigningKey(
	ctx context.Context,
	req *connect.Request[pb.UpdateScopedSigningKeyRequest],
) (*connect.Response[pb.UpdateScopedSigningKeyResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	key, err := h.service.UpdateScopedSigningKey(ctx, id, services.UpdateScopedSigningKeyRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateScopedSigningKeyResponse{
		Key: mappers.ScopedSigningKeyToProto(key),
	}), nil
}

// UpdatePermissions updates permissions for a scoped signing key
func (h *ScopedSigningKeyHandler) UpdatePermissions(
	ctx context.Context,
	req *connect.Request[pb.UpdatePermissionsRequest],
) (*connect.Response[pb.UpdatePermissionsResponse], error) {
	// TODO: The service doesn't have a separate UpdatePermissions method
	// Permissions need to be updated via UpdateScopedSigningKey or a new service method should be added
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// DeleteScopedSigningKey deletes a scoped signing key
func (h *ScopedSigningKeyHandler) DeleteScopedSigningKey(
	ctx context.Context,
	req *connect.Request[pb.DeleteScopedSigningKeyRequest],
) (*connect.Response[pb.DeleteScopedSigningKeyResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	err = h.service.DeleteScopedSigningKey(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.DeleteScopedSigningKeyResponse{}), nil
}
