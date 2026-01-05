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

// AccountHandler implements the AccountService gRPC service
type AccountHandler struct {
	service     *services.AccountService
	natsClient  interface{} // TODO: Will be replaced with NATS client for PushAccountJWT
}

// NewAccountHandler creates a new AccountHandler
func NewAccountHandler(service *services.AccountService) nisv1connect.AccountServiceHandler {
	return &AccountHandler{service: service}
}

// CreateAccount creates a new account
func (h *AccountHandler) CreateAccount(
	ctx context.Context,
	req *connect.Request[pb.CreateAccountRequest],
) (*connect.Response[pb.CreateAccountResponse], error) {
	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	enabled, maxMem, maxStor, maxStr, maxCons :=
		mappers.ProtoToJetStreamLimits(req.Msg.JetstreamLimits)

	account, err := h.service.CreateAccount(ctx, services.CreateAccountRequest{
		OperatorID:              operatorID,
		Name:                    req.Msg.Name,
		Description:             req.Msg.Description,
		JetStreamEnabled:        enabled,
		JetStreamMaxMemory:      maxMem,
		JetStreamMaxStorage:     maxStor,
		JetStreamMaxStreams:     maxStr,
		JetStreamMaxConsumers:   maxCons,
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
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	account, err := h.service.GetAccount(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
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
	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	account, err := h.service.GetAccountByName(ctx, operatorID, req.Msg.Name)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.GetAccountByNameResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// ListAccounts lists accounts for an operator
func (h *AccountHandler) ListAccounts(
	ctx context.Context,
	req *connect.Request[pb.ListAccountsRequest],
) (*connect.Response[pb.ListAccountsResponse], error) {
	// If operator_id is empty, list all accounts across all operators
	if req.Msg.OperatorId == "" {
		accounts, err := h.service.ListAllAccounts(ctx, mappers.ProtoToListOptions(req.Msg.Options))
		if err != nil {
			return nil, err
		}

		return connect.NewResponse(&pb.ListAccountsResponse{
			Accounts: mappers.AccountsToProto(accounts),
		}), nil
	}

	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	accounts, err := h.service.ListAccountsByOperator(ctx, operatorID, mappers.ProtoToListOptions(req.Msg.Options))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.ListAccountsResponse{
		Accounts: mappers.AccountsToProto(accounts),
	}), nil
}

// UpdateAccount updates an account
func (h *AccountHandler) UpdateAccount(
	ctx context.Context,
	req *connect.Request[pb.UpdateAccountRequest],
) (*connect.Response[pb.UpdateAccountResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	account, err := h.service.UpdateAccount(ctx, id, services.UpdateAccountRequest{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateAccountResponse{
		Account: mappers.AccountToProto(account),
	}), nil
}

// UpdateJetStreamLimits updates JetStream limits for an account
func (h *AccountHandler) UpdateJetStreamLimits(
	ctx context.Context,
	req *connect.Request[pb.UpdateJetStreamLimitsRequest],
) (*connect.Response[pb.UpdateJetStreamLimitsResponse], error) {
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
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
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
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
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	err = h.service.DeleteAccount(ctx, id)
	if err != nil {
		if err == repositories.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return connect.NewResponse(&pb.DeleteAccountResponse{}), nil
}

// PushAccountJWT pushes an account JWT to the NATS resolver
func (h *AccountHandler) PushAccountJWT(
	ctx context.Context,
	req *connect.Request[pb.PushAccountJWTRequest],
) (*connect.Response[pb.PushAccountJWTResponse], error) {
	// TODO: Implement when NATS client integration is added
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
