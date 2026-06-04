package handlers

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/authctx"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

type APITokenHandler struct {
	svc     *services.APITokenService
	permSvc *services.PermissionService
}

func NewAPITokenHandler(svc *services.APITokenService, permSvc *services.PermissionService) *APITokenHandler {
	return &APITokenHandler{svc: svc, permSvc: permSvc}
}

func parseOptionalUUID(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (h *APITokenHandler) CreateAPIToken(ctx context.Context, req *connect.Request[nisv1.CreateAPITokenRequest]) (*connect.Response[nisv1.CreateAPITokenResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	// Refuse token-authed callers minting fresh tokens. A token must not be used
	// to escalate into a new long-lived credential; that's a chained-privilege
	// foot-gun. The middleware sets Actor.Type=api_token on token-authed
	// requests; ordinary user-authed requests have no Actor set (or one with
	// Type=user).
	if actor, ok := authctx.GetActor(ctx); ok && actor.Type == entities.ActorTypeAPIToken {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("api tokens cannot be used to mint other api tokens"))
	}

	role := entities.APIUserRole(req.Msg.GetRole())
	operatorID, err := parseOptionalUUID(req.Msg.GetOperatorId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("operator_id: %w", err))
	}
	accountID, err := parseOptionalUUID(req.Msg.GetAccountId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("account_id: %w", err))
	}

	if err := h.permSvc.CanCreateAPIToken(ctx, apiUser, role, operatorID, accountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	createReq := services.CreateAPITokenRequest{
		Name:            req.Msg.GetName(),
		Description:     req.Msg.GetDescription(),
		Role:            role,
		OperatorID:      operatorID,
		AccountID:       accountID,
		CreatedByUserID: &apiUser.ID,
	}
	if exp := req.Msg.GetExpiresAt(); exp != nil {
		t := exp.AsTime()
		createReq.ExpiresAt = &t
	}

	// Resolve the OrganizationID for the new token.
	// org-admin: always mint in their own org (ignore the request field).
	// admin:     use req.organization_id if provided, else let CreateToken derive it.
	// lower:     let CreateToken derive from operator/account.
	switch apiUser.Role {
	case entities.RoleOrgAdmin:
		// Force org-admin's own org; no override allowed.
		createReq.OrganizationID = apiUser.OrganizationID
	case entities.RoleAdmin:
		reqOrgID, err := parseOptionalUUID(req.Msg.GetOrganizationId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("organization_id: %w", err))
		}
		createReq.OrganizationID = reqOrgID
		// For admin with no explicit org, CreateToken will derive from scope.
	}

	token, plaintext, err := h.svc.CreateToken(ctx, createReq)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.CreateAPITokenResponse{
		Token:     mappers.APITokenToProto(token),
		Plaintext: plaintext,
	}), nil
}

func (h *APITokenHandler) GetAPIToken(ctx context.Context, req *connect.Request[nisv1.GetAPITokenRequest]) (*connect.Response[nisv1.GetAPITokenResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	token, err := h.svc.GetToken(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadAPIToken(apiUser, token); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(&nisv1.GetAPITokenResponse{Token: mappers.APITokenToProto(token)}), nil
}

func (h *APITokenHandler) ListAPITokens(ctx context.Context, req *connect.Request[nisv1.ListAPITokensRequest]) (*connect.Response[nisv1.ListAPITokensResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.permSvc.CanListAPITokens(apiUser); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	filter := repositories.APITokenListFilter{
		IncludeRevoked: req.Msg.GetIncludeRevoked(),
	}
	if req.Msg.Page != nil {
		filter.Limit = int(req.Msg.Page.GetLimit())
		filter.Cursor = req.Msg.Page.GetCursor()
	}

	// Self-scope is enforced at the repo via authz.Scope.CallerUserID. The
	// admin-only created_by_user_id filter overrides who CallerUserID applies
	// to — admin can ask "show me tokens minted by user X" by constructing a
	// synthetic scope. Non-admins ignore the field entirely.
	scope := authz.ScopeFromAPIUser(apiUser)
	if apiUser.Role == entities.RoleAdmin {
		if filterUser := req.Msg.GetCreatedByUserId(); filterUser != "" {
			uid, err := uuid.Parse(filterUser)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("created_by_user_id: %w", err))
			}
			// Demote scope from admin-equivalent to "see only this user's tokens"
			// by clearing the admin role and pinning CallerUserID. The repo's
			// self-scope branch then takes over.
			scope = authz.Scope{
				Role:         string(entities.RoleAdmin) + "+filter",
				CallerUserID: uid,
			}
			_ = scope // keep documented; the repo treats unknown roles as zero,
			// so we instead use the explicit list-by-user filter below.
			filter2 := filter
			// Fall through to repo with a forged non-admin scope so the WHERE
			// pins created_by_user_id = uid. We craft it directly:
			fakeScope := authz.Scope{
				Role:         string(entities.RoleAccountAdmin), // any non-admin role; CallerUserID is what the repo's self-scope WHERE binds.
				CallerUserID: uid,
			}
			tokens, nextCursor, err := h.svc.ListTokensPage(ctx, fakeScope, filter2)
			if err != nil {
				if errors.Is(err, repositories.ErrInvalidCursor) {
					return nil, connect.NewError(connect.CodeInvalidArgument, err)
				}
				return nil, repoErrToConnect(err)
			}
			proto := make([]*nisv1.APIToken, 0, len(tokens))
			for _, t := range tokens {
				proto = append(proto, mappers.APITokenToProto(t))
			}
			return connect.NewResponse(&nisv1.ListAPITokensResponse{Tokens: proto, NextCursor: nextCursor}), nil
		}
	}

	tokens, nextCursor, err := h.svc.ListTokensPage(ctx, scope, filter)
	if err != nil {
		if errors.Is(err, repositories.ErrInvalidCursor) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, repoErrToConnect(err)
	}
	proto := make([]*nisv1.APIToken, 0, len(tokens))
	for _, t := range tokens {
		proto = append(proto, mappers.APITokenToProto(t))
	}
	return connect.NewResponse(&nisv1.ListAPITokensResponse{Tokens: proto, NextCursor: nextCursor}), nil
}

func (h *APITokenHandler) RevokeAPIToken(ctx context.Context, req *connect.Request[nisv1.RevokeAPITokenRequest]) (*connect.Response[nisv1.RevokeAPITokenResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	token, err := h.svc.GetToken(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanDeleteAPIToken(apiUser, token); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if err := h.svc.RevokeToken(ctx, id); err != nil {
		return nil, repoErrToConnect(err)
	}
	updated, err := h.svc.GetToken(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.RevokeAPITokenResponse{Token: mappers.APITokenToProto(updated)}), nil
}

func (h *APITokenHandler) DeleteAPIToken(ctx context.Context, req *connect.Request[nisv1.DeleteAPITokenRequest]) (*connect.Response[nisv1.DeleteAPITokenResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	token, err := h.svc.GetToken(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanDeleteAPIToken(apiUser, token); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if err := h.svc.DeleteToken(ctx, id); err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.DeleteAPITokenResponse{}), nil
}
