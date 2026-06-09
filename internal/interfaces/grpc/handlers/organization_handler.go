package handlers

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// OrganizationHandler implements nisv1connect.OrganizationServiceHandler.
type OrganizationHandler struct {
	svc       *services.OrganizationService
	permSvc   *services.PermissionService
	publicURL string
}

// NewOrganizationHandler creates a new OrganizationHandler. publicURL is the
// externally-reachable base URL (server.public_url); it is used to surface the
// OIDC callback_url in GetSSOConfig so operators can register it with the IdP.
func NewOrganizationHandler(svc *services.OrganizationService, permSvc *services.PermissionService, publicURL string) *OrganizationHandler {
	return &OrganizationHandler{svc: svc, permSvc: permSvc, publicURL: publicURL}
}

// CreateOrganization is KindRoleOnly: admin only.
func (h *OrganizationHandler) CreateOrganization(ctx context.Context, req *connect.Request[pb.CreateOrganizationRequest]) (*connect.Response[pb.CreateOrganizationResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	org, err := h.svc.CreateOrganization(ctx, services.CreateOrganizationRequest{
		Name:        req.Msg.GetName(),
		Slug:        req.Msg.GetSlug(),
		Description: req.Msg.GetDescription(),
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.CreateOrganizationResponse{
		Organization: mappers.OrganizationToProto(org),
	}), nil
}

// GetOrganization is KindPerRow: check CanReadOrganization.
func (h *OrganizationHandler) GetOrganization(ctx context.Context, req *connect.Request[pb.GetOrganizationRequest]) (*connect.Response[pb.GetOrganizationResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanReadOrganization(ctx, apiUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	org, err := h.svc.GetOrganization(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.GetOrganizationResponse{
		Organization: mappers.OrganizationToProto(org),
	}), nil
}

// GetOrganizationBySlug is KindPerRow: fetch then check CanReadOrganization.
func (h *OrganizationHandler) GetOrganizationBySlug(ctx context.Context, req *connect.Request[pb.GetOrganizationBySlugRequest]) (*connect.Response[pb.GetOrganizationBySlugResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	org, err := h.svc.GetOrganizationBySlug(ctx, req.Msg.GetSlug())
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadOrganization(ctx, apiUser, org.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(&pb.GetOrganizationBySlugResponse{
		Organization: mappers.OrganizationToProto(org),
	}), nil
}

// ListOrganizations is KindPerRow: narrows to caller's org unless admin.
func (h *OrganizationHandler) ListOrganizations(ctx context.Context, req *connect.Request[pb.ListOrganizationsRequest]) (*connect.Response[pb.ListOrganizationsResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.permSvc.CanListOrganizations(apiUser); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if apiUser.Role == entities.RoleAdmin {
		// Admin sees all.
		opts := repositories.ListOptions{}
		if req.Msg.GetOptions() != nil {
			opts = mappers.ProtoToListOptions(req.Msg.GetOptions())
		}
		if opts.Limit == 0 {
			opts.Limit = 200
		}
		orgs, err := h.svc.ListOrganizations(ctx, opts)
		if err != nil {
			return nil, repoErrToConnect(err)
		}
		proto := make([]*pb.Organization, 0, len(orgs))
		for _, o := range orgs {
			proto = append(proto, mappers.OrganizationToProto(o))
		}
		return connect.NewResponse(&pb.ListOrganizationsResponse{Organizations: proto}), nil
	}

	// Org-admin (or narrower role): return only their own org.
	if apiUser.OrganizationID == nil {
		return connect.NewResponse(&pb.ListOrganizationsResponse{}), nil
	}
	if err := h.permSvc.CanReadOrganization(ctx, apiUser, *apiUser.OrganizationID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	org, err := h.svc.GetOrganization(ctx, *apiUser.OrganizationID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.ListOrganizationsResponse{
		Organizations: []*pb.Organization{mappers.OrganizationToProto(org)},
	}), nil
}

// UpdateOrganization is KindPerRow: check CanUpdateOrganization.
func (h *OrganizationHandler) UpdateOrganization(ctx context.Context, req *connect.Request[pb.UpdateOrganizationRequest]) (*connect.Response[pb.UpdateOrganizationResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanUpdateOrganization(ctx, apiUser, id); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	org, err := h.svc.UpdateOrganization(ctx, id, services.UpdateOrganizationRequest{
		Name:        req.Msg.GetName(),
		Description: req.Msg.GetDescription(),
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.UpdateOrganizationResponse{
		Organization: mappers.OrganizationToProto(org),
	}), nil
}

// DeleteOrganization is KindRoleOnly: admin only.
func (h *OrganizationHandler) DeleteOrganization(ctx context.Context, req *connect.Request[pb.DeleteOrganizationRequest]) (*connect.Response[pb.DeleteOrganizationResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.svc.DeleteOrganization(ctx, id); err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.DeleteOrganizationResponse{}), nil
}

// GetSSOConfig is KindPerRow: check CanReadSSO.
func (h *OrganizationHandler) GetSSOConfig(ctx context.Context, req *connect.Request[pb.GetSSOConfigRequest]) (*connect.Response[pb.GetSSOConfigResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := uuid.Parse(req.Msg.GetOrganizationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanReadSSO(ctx, apiUser, orgID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	// A missing SSO config is not an error here: the operator needs the
	// callback_url (derived purely from server.public_url) *before* they can
	// save an SSO config, so they can register that redirect URI with their
	// IdP first. Return an empty Config with the callback_url in that case;
	// only genuine errors propagate.
	cfg, err := h.svc.GetSSOConfig(ctx, orgID)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, repoErrToConnect(err)
	}
	callbackURL := ""
	if h.publicURL != "" {
		callbackURL = h.publicURL + "/auth/oidc/callback"
	}
	return connect.NewResponse(&pb.GetSSOConfigResponse{
		Config:      mappers.SSOConfigToProto(cfg),
		CallbackUrl: callbackURL,
	}), nil
}

// SetSSOConfig is KindPerRow: check CanManageSSO.
func (h *OrganizationHandler) SetSSOConfig(ctx context.Context, req *connect.Request[pb.SetSSOConfigRequest]) (*connect.Response[pb.SetSSOConfigResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := uuid.Parse(req.Msg.GetOrganizationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanManageSSO(ctx, apiUser, orgID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	var defaultRole *entities.APIUserRole
	if s := req.Msg.GetDefaultRole(); s != "" {
		r := entities.APIUserRole(s)
		defaultRole = &r
	}

	svcReq := services.SetSSOConfigRequest{
		OrganizationID: orgID,
		Enabled:        req.Msg.GetEnabled(),
		IssuerURL:      req.Msg.GetIssuerUrl(),
		ClientID:       req.Msg.GetClientId(),
		ClientSecret:   req.Msg.GetClientSecret(),
		Scopes:         req.Msg.GetScopes(),
		GroupClaim:     req.Msg.GetGroupClaim(),
		DefaultRole:    defaultRole,
	}
	cfg, err := h.svc.SetSSOConfig(ctx, svcReq)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.SetSSOConfigResponse{
		Config: mappers.SSOConfigToProto(cfg),
	}), nil
}

// DeleteSSOConfig is KindPerRow: check CanManageSSO.
func (h *OrganizationHandler) DeleteSSOConfig(ctx context.Context, req *connect.Request[pb.DeleteSSOConfigRequest]) (*connect.Response[pb.DeleteSSOConfigResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := uuid.Parse(req.Msg.GetOrganizationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanManageSSO(ctx, apiUser, orgID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if err := h.svc.DeleteSSOConfig(ctx, orgID); err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.DeleteSSOConfigResponse{}), nil
}

// ListSSORoleMappings is KindPerRow: check CanReadSSO.
func (h *OrganizationHandler) ListSSORoleMappings(ctx context.Context, req *connect.Request[pb.ListSSORoleMappingsRequest]) (*connect.Response[pb.ListSSORoleMappingsResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := uuid.Parse(req.Msg.GetOrganizationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanReadSSO(ctx, apiUser, orgID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	mappings, err := h.svc.ListSSORoleMappings(ctx, orgID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	proto := make([]*pb.SSORoleMapping, 0, len(mappings))
	for _, m := range mappings {
		proto = append(proto, mappers.SSORoleMappingToProto(m))
	}
	return connect.NewResponse(&pb.ListSSORoleMappingsResponse{Mappings: proto}), nil
}

// SetSSORoleMappings is KindPerRow: check CanManageSSO.
func (h *OrganizationHandler) SetSSORoleMappings(ctx context.Context, req *connect.Request[pb.SetSSORoleMappingsRequest]) (*connect.Response[pb.SetSSORoleMappingsResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := uuid.Parse(req.Msg.GetOrganizationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanManageSSO(ctx, apiUser, orgID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	inputs := make([]services.SSORoleMappingInput, 0, len(req.Msg.GetMappings()))
	for i, m := range req.Msg.GetMappings() {
		scopeOpID, err := parseOptionalUUID(m.GetScopeOperatorId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mapping[%d].scope_operator_id: %w", i, err))
		}
		scopeAccID, err := parseOptionalUUID(m.GetScopeAccountId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mapping[%d].scope_account_id: %w", i, err))
		}
		inputs = append(inputs, services.SSORoleMappingInput{
			GroupValue:      m.GetGroupValue(),
			Role:            entities.APIUserRole(m.GetRole()),
			ScopeOperatorID: scopeOpID,
			ScopeAccountID:  scopeAccID,
			Priority:        int(m.GetPriority()),
		})
	}

	mappings, err := h.svc.SetSSORoleMappings(ctx, orgID, inputs)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	proto := make([]*pb.SSORoleMapping, 0, len(mappings))
	for _, m := range mappings {
		proto = append(proto, mappers.SSORoleMappingToProto(m))
	}
	return connect.NewResponse(&pb.SetSSORoleMappingsResponse{Mappings: proto}), nil
}
