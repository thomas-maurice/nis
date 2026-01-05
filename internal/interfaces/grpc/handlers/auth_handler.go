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

// AuthHandler implements the AuthService gRPC service
type AuthHandler struct {
	service *services.AuthService
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(service *services.AuthService) nisv1connect.AuthServiceHandler {
	return &AuthHandler{service: service}
}

// Login authenticates a user and returns a token
func (h *AuthHandler) Login(
	ctx context.Context,
	req *connect.Request[pb.LoginRequest],
) (*connect.Response[pb.LoginResponse], error) {
	resp, err := h.service.Login(ctx, services.LoginRequest{
		Username: req.Msg.Username,
		Password: req.Msg.Password,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	return connect.NewResponse(&pb.LoginResponse{
		Token: resp.Token,
		User:  mappers.APIUserToProto(resp.User),
	}), nil
}

// ValidateToken validates an authentication token
func (h *AuthHandler) ValidateToken(
	ctx context.Context,
	req *connect.Request[pb.ValidateTokenRequest],
) (*connect.Response[pb.ValidateTokenResponse], error) {
	user, err := h.service.ValidateToken(ctx, req.Msg.Token)
	if err != nil {
		return connect.NewResponse(&pb.ValidateTokenResponse{
			Valid: false,
			User:  nil,
		}), nil
	}

	return connect.NewResponse(&pb.ValidateTokenResponse{
		Valid: true,
		User:  mappers.APIUserToProto(user),
	}), nil
}

// CreateAPIUser creates a new API user
func (h *AuthHandler) CreateAPIUser(
	ctx context.Context,
	req *connect.Request[pb.CreateAPIUserRequest],
) (*connect.Response[pb.CreateAPIUserResponse], error) {
	role := mappers.ProtoToAPIUserRole(req.Msg.Permissions)

	user, err := h.service.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: req.Msg.Username,
		Password: req.Msg.Password,
		Role:     role,
	})
	if err != nil {
		if err == repositories.ErrAlreadyExists {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.CreateAPIUserResponse{
		User: mappers.APIUserToProto(user),
	}), nil
}

// GetAPIUser retrieves an API user by ID
func (h *AuthHandler) GetAPIUser(
	ctx context.Context,
	req *connect.Request[pb.GetAPIUserRequest],
) (*connect.Response[pb.GetAPIUserResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.GetAPIUser(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetAPIUserResponse{
		User: mappers.APIUserToProto(user),
	}), nil
}

// GetAPIUserByUsername retrieves an API user by username
func (h *AuthHandler) GetAPIUserByUsername(
	ctx context.Context,
	req *connect.Request[pb.GetAPIUserByUsernameRequest],
) (*connect.Response[pb.GetAPIUserByUsernameResponse], error) {
	user, err := h.service.GetAPIUserByUsername(ctx, req.Msg.Username)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetAPIUserByUsernameResponse{
		User: mappers.APIUserToProto(user),
	}), nil
}

// ListAPIUsers lists all API users
func (h *AuthHandler) ListAPIUsers(
	ctx context.Context,
	req *connect.Request[pb.ListAPIUsersRequest],
) (*connect.Response[pb.ListAPIUsersResponse], error) {
	users, err := h.service.ListAPIUsers(ctx)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ListAPIUsersResponse{
		Users: mappers.APIUsersToProto(users),
	}), nil
}

// UpdateAPIUserPassword updates an API user's password
func (h *AuthHandler) UpdateAPIUserPassword(
	ctx context.Context,
	req *connect.Request[pb.UpdateAPIUserPasswordRequest],
) (*connect.Response[pb.UpdateAPIUserPasswordResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.UpdateAPIUserPassword(ctx, id, services.UpdatePasswordRequest{
		Password: req.Msg.Password,
	})
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateAPIUserPasswordResponse{
		User: mappers.APIUserToProto(user),
	}), nil
}

// UpdateAPIUserPermissions updates an API user's permissions
func (h *AuthHandler) UpdateAPIUserPermissions(
	ctx context.Context,
	req *connect.Request[pb.UpdateAPIUserPermissionsRequest],
) (*connect.Response[pb.UpdateAPIUserPermissionsResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	role := mappers.ProtoToAPIUserRole(req.Msg.Permissions)

	user, err := h.service.UpdateAPIUserRole(ctx, id, services.UpdateRoleRequest{
		Role: role,
	})
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateAPIUserPermissionsResponse{
		User: mappers.APIUserToProto(user),
	}), nil
}

// DeleteAPIUser deletes an API user
func (h *AuthHandler) DeleteAPIUser(
	ctx context.Context,
	req *connect.Request[pb.DeleteAPIUserRequest],
) (*connect.Response[pb.DeleteAPIUserResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	err = h.service.DeleteAPIUser(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.DeleteAPIUserResponse{}), nil
}
