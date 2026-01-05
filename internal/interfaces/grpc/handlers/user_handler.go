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

// UserHandler implements the UserService gRPC service
type UserHandler struct {
	service *services.UserService
}

// NewUserHandler creates a new UserHandler
func NewUserHandler(service *services.UserService) nisv1connect.UserServiceHandler {
	return &UserHandler{service: service}
}

// CreateUser creates a new user
func (h *UserHandler) CreateUser(
	ctx context.Context,
	req *connect.Request[pb.CreateUserRequest],
) (*connect.Response[pb.CreateUserResponse], error) {
	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	scopedKeyID, err := mappers.ProtoToScopedKeyID(req.Msg.ScopedSigningKeyId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.CreateUser(ctx, services.CreateUserRequest{
		AccountID:           accountID,
		Name:                req.Msg.Name,
		Description:         req.Msg.Description,
		ScopedSigningKeyID:  scopedKeyID,
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CreateUserResponse{
		User: mappers.UserToProto(user),
	}), nil
}

// GetUser retrieves a user by ID
func (h *UserHandler) GetUser(
	ctx context.Context,
	req *connect.Request[pb.GetUserRequest],
) (*connect.Response[pb.GetUserResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.GetUser(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetUserResponse{
		User: mappers.UserToProto(user),
	}), nil
}

// GetUserByName retrieves a user by name
func (h *UserHandler) GetUserByName(
	ctx context.Context,
	req *connect.Request[pb.GetUserByNameRequest],
) (*connect.Response[pb.GetUserByNameResponse], error) {
	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.GetUserByName(ctx, accountID, req.Msg.Name)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetUserByNameResponse{
		User: mappers.UserToProto(user),
	}), nil
}

// ListUsers lists users for an account
func (h *UserHandler) ListUsers(
	ctx context.Context,
	req *connect.Request[pb.ListUsersRequest],
) (*connect.Response[pb.ListUsersResponse], error) {
	// If account_id is empty, list all users across all accounts
	if req.Msg.AccountId == "" {
		users, err := h.service.ListAllUsers(ctx, mappers.ProtoToListOptions(req.Msg.Options))
		if err != nil {
			return nil, err
		}

		return connect.NewResponse(&pb.ListUsersResponse{
			Users: mappers.UsersToProto(users),
		}), nil
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	users, err := h.service.ListUsersByAccount(ctx, accountID, mappers.ProtoToListOptions(req.Msg.Options))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ListUsersResponse{
		Users: mappers.UsersToProto(users),
	}), nil
}

// UpdateUser updates a user
func (h *UserHandler) UpdateUser(
	ctx context.Context,
	req *connect.Request[pb.UpdateUserRequest],
) (*connect.Response[pb.UpdateUserResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.UpdateUser(ctx, id, services.UpdateUserRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateUserResponse{
		User: mappers.UserToProto(user),
	}), nil
}

// DeleteUser deletes a user
func (h *UserHandler) DeleteUser(
	ctx context.Context,
	req *connect.Request[pb.DeleteUserRequest],
) (*connect.Response[pb.DeleteUserResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	err = h.service.DeleteUser(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.DeleteUserResponse{}), nil
}

// GetUserCredentials retrieves user credentials file
func (h *UserHandler) GetUserCredentials(
	ctx context.Context,
	req *connect.Request[pb.GetUserCredentialsRequest],
) (*connect.Response[pb.GetUserCredentialsResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	creds, err := h.service.GetUserCredentials(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetUserCredentialsResponse{
		Credentials: creds,
	}), nil
}
