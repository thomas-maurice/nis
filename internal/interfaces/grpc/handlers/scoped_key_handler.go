package handlers

import (
	"context"
	"time"

	"connectrpc.com/connect"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// ScopedSigningKeyHandler implements the ScopedSigningKeyService gRPC service
type ScopedSigningKeyHandler struct {
	service     *services.ScopedSigningKeyService
	permService *services.PermissionService
}

// NewScopedSigningKeyHandler creates a new ScopedSigningKeyHandler
func NewScopedSigningKeyHandler(service *services.ScopedSigningKeyService, permService *services.PermissionService) nisv1connect.ScopedSigningKeyServiceHandler {
	return &ScopedSigningKeyHandler{
		service:     service,
		permService: permService,
	}
}

// CreateScopedSigningKey creates a new scoped signing key
func (h *ScopedSigningKeyHandler) CreateScopedSigningKey(
	ctx context.Context,
	req *connect.Request[pb.CreateScopedSigningKeyRequest],
) (*connect.Response[pb.CreateScopedSigningKeyResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to update this account (creating scoped keys is an account-level permission)
	if err := h.permService.CanUpdateAccount(ctx, requestingUser, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
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
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	key, err := h.service.GetScopedSigningKey(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to read the account that owns this key
	if err := h.permService.CanReadAccount(ctx, requestingUser, key.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
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
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this account
	if err := h.permService.CanReadAccount(ctx, requestingUser, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	key, err := h.service.GetScopedSigningKeyByName(ctx, accountID, req.Msg.Name)
	if err != nil {
		return nil, repoErrToConnect(err)
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
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	// If account_id is empty, list all scoped signing keys (filtered by permissions)
	if req.Msg.AccountId == "" {
		keys, err := h.service.ListAllScopedSigningKeys(ctx, mappers.ProtoToListOptions(req.Msg.Options))
		if err != nil {
			return nil, err
		}
		// Filter keys based on account permissions
		var filteredKeys []*entities.ScopedSigningKey
		for _, key := range keys {
			if err := h.permService.CanReadAccount(ctx, requestingUser, key.AccountID); err == nil {
				filteredKeys = append(filteredKeys, key)
			}
		}
		return connect.NewResponse(&pb.ListScopedSigningKeysResponse{
			Keys: mappers.ScopedSigningKeysToProto(filteredKeys),
		}), nil
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this account
	if err := h.permService.CanReadAccount(ctx, requestingUser, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
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
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the key to check which account it belongs to
	existingKey, err := h.service.GetScopedSigningKey(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to update the account that owns this key
	if err := h.permService.CanUpdateAccount(ctx, requestingUser, existingKey.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	key, err := h.service.UpdateScopedSigningKey(ctx, id, services.UpdateScopedSigningKeyRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.UpdateScopedSigningKeyResponse{
		Key: mappers.ScopedSigningKeyToProto(key),
	}), nil
}

// UpdatePermissions updates the pub/sub allow/deny lists and response permission
// of a scoped signing key. The underlying service treats this as an UpdateScoped
// SigningKey call with permission fields populated, and re-signs the parent
// account JWT so NATS picks up the new template on the next cluster sync.
func (h *ScopedSigningKeyHandler) UpdatePermissions(
	ctx context.Context,
	req *connect.Request[pb.UpdatePermissionsRequest],
) (*connect.Response[pb.UpdatePermissionsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	existingKey, err := h.service.GetScopedSigningKey(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	if err := h.permService.CanUpdateAccount(ctx, requestingUser, existingKey.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	updateReq := services.UpdateScopedSigningKeyRequest{}
	if perms := req.Msg.Permissions; perms != nil {
		// Always replace the lists (nil means "leave alone" per UpdateScopedSigningKey
		// semantics — but the caller of UpdatePermissions intends to set them, so we
		// pass through whatever they sent, treating nil slices as "clear this list").
		updateReq.PubAllow = perms.PubAllow
		if updateReq.PubAllow == nil {
			updateReq.PubAllow = []string{}
		}
		updateReq.PubDeny = perms.PubDeny
		if updateReq.PubDeny == nil {
			updateReq.PubDeny = []string{}
		}
		updateReq.SubAllow = perms.SubAllow
		if updateReq.SubAllow == nil {
			updateReq.SubAllow = []string{}
		}
		updateReq.SubDeny = perms.SubDeny
		if updateReq.SubDeny == nil {
			updateReq.SubDeny = []string{}
		}
	}
	if resp := req.Msg.ResponsePermission; resp != nil {
		maxMsgs := int(resp.MaxMsgs)
		expires := time.Duration(resp.Expires) * time.Nanosecond
		updateReq.ResponseMaxMsgs = &maxMsgs
		updateReq.ResponseTTL = &expires
	}

	updated, err := h.service.UpdateScopedSigningKey(ctx, id, updateReq)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.UpdatePermissionsResponse{
		Key: mappers.ScopedSigningKeyToProto(updated),
	}), nil
}

// DeleteScopedSigningKey deletes a scoped signing key
func (h *ScopedSigningKeyHandler) DeleteScopedSigningKey(
	ctx context.Context,
	req *connect.Request[pb.DeleteScopedSigningKeyRequest],
) (*connect.Response[pb.DeleteScopedSigningKeyResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the key to check which account it belongs to
	existingKey, err := h.service.GetScopedSigningKey(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to update the account that owns this key
	if err := h.permService.CanUpdateAccount(ctx, requestingUser, existingKey.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	err = h.service.DeleteScopedSigningKey(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.DeleteScopedSigningKeyResponse{}), nil
}
