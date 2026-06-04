package handlers

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// AccountHandler implements the AccountService gRPC service
type AccountHandler struct {
	service     *services.AccountService
	permService *services.PermissionService
}

// NewAccountHandler creates a new AccountHandler
func NewAccountHandler(service *services.AccountService, permService *services.PermissionService) nisv1connect.AccountServiceHandler {
	return &AccountHandler{
		service:     service,
		permService: permService,
	}
}

// CreateAccount creates a new account
func (h *AccountHandler) CreateAccount(
	ctx context.Context,
	req *connect.Request[pb.CreateAccountRequest],
) (*connect.Response[pb.CreateAccountResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to create account in this operator
	if err := h.permService.CanCreateAccount(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	enabled, maxMem, maxStor, maxStr, maxCons :=
		mappers.ProtoToJetStreamLimits(req.Msg.JetstreamLimits)

	account, err := h.service.CreateAccount(ctx, services.CreateAccountRequest{
		OperatorID:            operatorID,
		Name:                  req.Msg.Name,
		Description:           req.Msg.Description,
		JetStreamEnabled:      enabled,
		JetStreamMaxMemory:    maxMem,
		JetStreamMaxStorage:   maxStor,
		JetStreamMaxStreams:   maxStr,
		JetStreamMaxConsumers: maxCons,
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CreateAccountResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// GetAccount retrieves an account by ID
func (h *AccountHandler) GetAccount(
	ctx context.Context,
	req *connect.Request[pb.GetAccountRequest],
) (*connect.Response[pb.GetAccountResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Check permission to read this account
	if err := h.permService.CanReadAccount(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	account, err := h.service.GetAccount(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.GetAccountResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// GetAccountByName retrieves an account by name
func (h *AccountHandler) GetAccountByName(
	ctx context.Context,
	req *connect.Request[pb.GetAccountByNameRequest],
) (*connect.Response[pb.GetAccountByNameResponse], error) {
	// Get requesting user from context
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	account, err := h.service.GetAccountByName(ctx, operatorID, req.Msg.Name)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	// Check permission to read this account
	if err := h.permService.CanReadAccount(ctx, requestingUser, account.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&pb.GetAccountByNameResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// ListAccounts lists accounts with cursor pagination and SQL-level scope enforcement.
func (h *AccountHandler) ListAccounts(
	ctx context.Context,
	req *connect.Request[pb.ListAccountsRequest],
) (*connect.Response[pb.ListAccountsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	scope := authz.ScopeFromAPIUser(requestingUser)

	filter := repositories.AccountListFilter{
		NameLike: strings.TrimSpace(req.Msg.NameLike),
	}
	if req.Msg.Page != nil {
		filter.Limit = int(req.Msg.Page.Limit)
		filter.Cursor = req.Msg.Page.Cursor
	}

	// Apply optional operator_id filter.
	if req.Msg.OperatorId != "" {
		operatorID, parseErr := mappers.ParseUUID(req.Msg.OperatorId)
		if parseErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, parseErr)
		}
		filter.OperatorID = &operatorID
	}

	accounts, nextCursor, err := h.service.ListAccountsPage(ctx, scope, filter)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&pb.ListAccountsResponse{
		Accounts:   mappers.AccountsToProto(accounts),
		NextCursor: nextCursor,
	}), nil
}

// UpdateAccount updates an account
func (h *AccountHandler) UpdateAccount(
	ctx context.Context,
	req *connect.Request[pb.UpdateAccountRequest],
) (*connect.Response[pb.UpdateAccountResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.permService.CanUpdateAccount(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	account, err := h.service.UpdateAccount(ctx, id, services.UpdateAccountRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.UpdateAccountResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// UpdateJetStreamLimits updates JetStream limits for an account. JetStream
// limits gate per-account memory/storage/streams/consumers — the same authority
// as a plain UpdateAccount, plus a quota / resource-exhaustion vector if the
// per-row check is skipped.
func (h *AccountHandler) UpdateJetStreamLimits(
	ctx context.Context,
	req *connect.Request[pb.UpdateJetStreamLimitsRequest],
) (*connect.Response[pb.UpdateJetStreamLimitsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.permService.CanUpdateAccount(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	enabled, maxMem, maxStor, maxStr, maxCons :=
		mappers.ProtoToJetStreamLimits(req.Msg.Limits)

	account, err := h.service.UpdateJetStreamLimits(ctx, id, services.UpdateJetStreamLimitsRequest{
		Enabled:      enabled,
		MaxMemory:    maxMem,
		MaxStorage:   maxStor,
		MaxStreams:   maxStr,
		MaxConsumers: maxCons,
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.UpdateJetStreamLimitsResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// DeleteAccount deletes an account
func (h *AccountHandler) DeleteAccount(
	ctx context.Context,
	req *connect.Request[pb.DeleteAccountRequest],
) (*connect.Response[pb.DeleteAccountResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.permService.CanDeleteAccount(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	err = h.service.DeleteAccount(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.DeleteAccountResponse{}), nil
}

// PushAccountJWT pushes an account JWT to the NATS resolver. The cluster
// auto-sync substrate (A13-full) already pushes account JWTs in-tx after every
// mutation, so this RPC has no remaining use case and is intentionally
// unimplemented. The auth preamble is included so the handler-RBAC lint and
// future implementers see the gate.
func (h *AccountHandler) PushAccountJWT(
	ctx context.Context,
	req *connect.Request[pb.PushAccountJWTRequest],
) (*connect.Response[pb.PushAccountJWTResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.permService.CanUpdateAccount(ctx, requestingUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// ListAccountJWTRevocations returns the active revocation entries currently
// flattened into the account JWT's NATS Revocations map. These are the
// entries NATS actually rejects on — distinct from users flagged via
// users.revoked_at, which RegenerateUserCredentials clears.
func (h *AccountHandler) ListAccountJWTRevocations(
	ctx context.Context,
	req *connect.Request[pb.ListAccountJWTRevocationsRequest],
) (*connect.Response[pb.ListAccountJWTRevocationsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.permService.CanReadAccount(ctx, requestingUser, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	views, err := h.service.ListJWTRevocations(ctx, accountID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.ListAccountJWTRevocationsResponse{
		Revocations: mappers.AccountJWTRevocationViewsToProto(views),
	}), nil
}

// GetAccountJetStreamUsage probes every cluster attached to the account's
// operator over NATS and returns per-cluster live JetStream usage (memory,
// storage, streams, consumers). Read-only — no DB mutations, no events. The
// service returns per-cluster status fields rather than a single error so the
// UI can show partial results when one cluster of many is unreachable.
func (h *AccountHandler) GetAccountJetStreamUsage(
	ctx context.Context,
	req *connect.Request[pb.GetAccountJetStreamUsageRequest],
) (*connect.Response[pb.GetAccountJetStreamUsageResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	accountID, err := mappers.ParseUUID(req.Msg.AccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.permService.CanReadAccount(ctx, requestingUser, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	results, err := h.service.GetAccountJetStreamUsage(ctx, accountID, services.GetAccountJetStreamUsageOptions{
		IncludeUnhealthy: req.Msg.IncludeUnhealthy,
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	return connect.NewResponse(&pb.GetAccountJetStreamUsageResponse{
		Clusters: mappers.ClusterJetStreamUsagesToProto(results),
	}), nil
}
