package handlers

import (
	"context"
	"errors"
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

// UserHandler implements the UserService gRPC service
type UserHandler struct {
	service        *services.UserService
	permService    *services.PermissionService
	revocationSvc  *services.UserRevocationService
}

// NewUserHandler creates a new UserHandler
func NewUserHandler(service *services.UserService, permService *services.PermissionService, revocationSvc *services.UserRevocationService) nisv1connect.UserServiceHandler {
	return &UserHandler{
		service:       service,
		permService:   permService,
		revocationSvc: revocationSvc,
	}
}

// CreateUser creates a new user
func (h *UserHandler) CreateUser(
	ctx context.Context,
	req *connect.Request[pb.CreateUserRequest],
) (*connect.Response[pb.CreateUserResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to update this account (creating users is an account-level permission)
	if err := h.permService.CanUpdateAccount(ctx, requestingUser, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	scopedKeyID, err := mappers.ProtoToScopedKeyID(req.Msg.ScopedSigningKeyId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.CreateUser(ctx, services.CreateUserRequest{
		AccountID:          accountID,
		Name:               req.Msg.Name,
		Description:        req.Msg.Description,
		ScopedSigningKeyID: scopedKeyID,
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
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	user, err := h.service.GetUser(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to read this user
	if err := h.permService.CanReadUser(ctx, requestingUser, user.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
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

	user, err := h.service.GetUserByName(ctx, accountID, req.Msg.Name)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.GetUserByNameResponse{
		User: mappers.UserToProto(user),
	}), nil
}

// ListUsers lists users with cursor pagination and SQL-level scope enforcement.
func (h *UserHandler) ListUsers(
	ctx context.Context,
	req *connect.Request[pb.ListUsersRequest],
) (*connect.Response[pb.ListUsersResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	scope := authz.ScopeFromAPIUser(requestingUser)

	filter := repositories.UserListFilter{
		NameLike: strings.TrimSpace(req.Msg.NameLike),
	}
	if req.Msg.Page != nil {
		filter.Limit = int(req.Msg.Page.Limit)
		filter.Cursor = req.Msg.Page.Cursor
	}

	// Apply optional account_id filter.
	if req.Msg.AccountId != "" {
		accountID, parseErr := mappers.ParseUUID(req.Msg.AccountId)
		if parseErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, parseErr)
		}
		filter.AccountID = &accountID
	}

	users, nextCursor, err := h.service.ListUsersPage(ctx, scope, filter)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&pb.ListUsersResponse{
		Users:      mappers.UsersToProto(users),
		NextCursor: nextCursor,
	}), nil
}

// UpdateUser updates a user
func (h *UserHandler) UpdateUser(
	ctx context.Context,
	req *connect.Request[pb.UpdateUserRequest],
) (*connect.Response[pb.UpdateUserResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the user to check which account it belongs to
	existingUser, err := h.service.GetUser(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to update the account that owns this user
	if err := h.permService.CanUpdateAccount(ctx, requestingUser, existingUser.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	updateReq := services.UpdateUserRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	}
	// Two semantics for clearing/setting the TTL override; see proto comments.
	switch {
	case req.Msg.ClearJwtTtl:
		updateReq.SetJWTTTL = true
		updateReq.JWTTTL = nil
	case req.Msg.JwtTtlSeconds != nil:
		d := time.Duration(*req.Msg.JwtTtlSeconds) * time.Second
		updateReq.SetJWTTTL = true
		updateReq.JWTTTL = &d
	}

	user, err := h.service.UpdateUser(ctx, id, updateReq)
	if err != nil {
		return nil, repoErrToConnect(err)
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
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the user to check which account it belongs to
	existingUser, err := h.service.GetUser(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to update the account that owns this user
	if err := h.permService.CanUpdateAccount(ctx, requestingUser, existingUser.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	err = h.service.DeleteUser(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.DeleteUserResponse{}), nil
}

// GetUserCredentials retrieves user credentials file
func (h *UserHandler) GetUserCredentials(
	ctx context.Context,
	req *connect.Request[pb.GetUserCredentialsRequest],
) (*connect.Response[pb.GetUserCredentialsResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// First get the user to check permissions
	user, err := h.service.GetUser(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to read this user
	if err := h.permService.CanReadUser(ctx, requestingUser, user.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	creds, err := h.service.GetUserCredentials(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.GetUserCredentialsResponse{
		Credentials: creds,
	}), nil
}

// RevokeUser revokes a user's NATS credential via the parent account JWT's
// Revocations map. See P2 in PROPOSALS.md.
func (h *UserHandler) RevokeUser(
	ctx context.Context,
	req *connect.Request[pb.RevokeUserRequest],
) (*connect.Response[pb.RevokeUserResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permService.CanRevokeUser(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	user, err := h.revocationSvc.RevokeUser(ctx, id, req.Msg.Reason)
	if err != nil {
		if errors.Is(err, services.ErrUserAlreadyRevoked) {
			// Idempotent: return current state. Clients can tell from
			// user.revoked_at that the revocation was a no-op.
			return connect.NewResponse(&pb.RevokeUserResponse{
				User: mappers.UserToProto(user),
			}), nil
		}
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.RevokeUserResponse{
		User: mappers.UserToProto(user),
	}), nil
}

// RegenerateUserCredentials mints a fresh user JWT (replaces the previous one)
// and returns the new .creds file in the response. Clears revoked_at if set.
func (h *UserHandler) RegenerateUserCredentials(
	ctx context.Context,
	req *connect.Request[pb.RegenerateUserCredentialsRequest],
) (*connect.Response[pb.RegenerateUserCredentialsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permService.CanRegenerateUserCredentials(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	user, err := h.revocationSvc.RegenerateUserJWT(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	creds, err := h.service.GetUserCredentials(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.RegenerateUserCredentialsResponse{
		User:        mappers.UserToProto(user),
		Credentials: creds,
	}), nil
}
