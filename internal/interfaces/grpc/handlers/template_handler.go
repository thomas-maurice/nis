package handlers

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// TemplateHandler implements TemplateService. Thin adapter — auth +
// proto mapping + repo-error translation. All business logic lives in
// services.TemplateService.
//
// Permission shape: every read goes through CanReadTemplate, every
// mutation through CanManageTemplate, and ApplyTemplateToScopedKey
// additionally checks CanManageScopedKeys on the target SSK's account
// (the apply operates on both the template's dependent set AND the
// SSK's permission columns, so authority on either side is required).
type TemplateHandler struct {
	service     *services.TemplateService
	sskService  *services.ScopedSigningKeyService
	permService *services.PermissionService
	factory     factoryForLookups
}

// factoryForLookups is the small slice of persistence.RepositoryFactory
// the handler needs for cross-entity lookups (operator resolution from
// template, account resolution from SSK). Keeping it narrow avoids
// growing the constructor surface.
type factoryForLookups interface {
	TemplateRepository() repositories.TemplateRepository
	ScopedSigningKeyRepository() repositories.ScopedSigningKeyRepository
	AccountRepository() repositories.AccountRepository
}

func NewTemplateHandler(
	service *services.TemplateService,
	sskService *services.ScopedSigningKeyService,
	permService *services.PermissionService,
	factory factoryForLookups,
) nisv1connect.TemplateServiceHandler {
	return &TemplateHandler{service: service, sskService: sskService, permService: permService, factory: factory}
}

func (h *TemplateHandler) CreateTemplate(ctx context.Context, req *connect.Request[pb.CreateTemplateRequest]) (*connect.Response[pb.CreateTemplateResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permService.CanManageTemplate(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	pubAllow, pubDeny, subAllow, subDeny := mappers.ProtoToUserPermissions(req.Msg.Permissions)
	respMax, respExp := mappers.ProtoToResponsePermission(req.Msg.ResponsePermission)

	tpl, ver, err := h.service.CreateTemplate(ctx, services.CreateTemplateRequest{
		OperatorID:      operatorID,
		Name:            req.Msg.Name,
		Description:     req.Msg.Description,
		PubAllow:        pubAllow,
		PubDeny:         pubDeny,
		SubAllow:        subAllow,
		SubDeny:         subDeny,
		ResponseMaxMsgs: respMax,
		ResponseTTL:     time.Duration(respExp),
		ChangeNote:      req.Msg.ChangeNote,
		CreatedByUserID: &requestingUser.ID,
	})
	if err != nil {
		return nil, mapTemplateError(err)
	}
	return connect.NewResponse(&pb.CreateTemplateResponse{
		Template: mappers.TemplateToProto(tpl),
		Version:  mappers.TemplateVersionToProto(ver),
	}), nil
}

func (h *TemplateHandler) GetTemplate(ctx context.Context, req *connect.Request[pb.GetTemplateRequest]) (*connect.Response[pb.GetTemplateResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tpl, ver, err := h.service.GetTemplate(ctx, id, int(req.Msg.VersionNumber))
	if err != nil {
		return nil, mapTemplateError(err)
	}
	if err := h.permService.CanReadTemplate(ctx, requestingUser, tpl.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(&pb.GetTemplateResponse{
		Template: mappers.TemplateToProto(tpl),
		Version:  mappers.TemplateVersionToProto(ver),
	}), nil
}

func (h *TemplateHandler) GetTemplateByName(ctx context.Context, req *connect.Request[pb.GetTemplateByNameRequest]) (*connect.Response[pb.GetTemplateByNameResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permService.CanReadTemplate(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	tpl, ver, err := h.service.GetTemplateByName(ctx, operatorID, req.Msg.Name, int(req.Msg.VersionNumber))
	if err != nil {
		return nil, mapTemplateError(err)
	}
	return connect.NewResponse(&pb.GetTemplateByNameResponse{
		Template: mappers.TemplateToProto(tpl),
		Version:  mappers.TemplateVersionToProto(ver),
	}), nil
}

func (h *TemplateHandler) ListTemplates(ctx context.Context, req *connect.Request[pb.ListTemplatesRequest]) (*connect.Response[pb.ListTemplatesResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := mappers.ParseUUID(req.Msg.OperatorId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permService.CanReadTemplate(ctx, requestingUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	opts := repositories.ListOptions{}
	if req.Msg.Options != nil {
		opts.Limit = int(req.Msg.Options.Limit)
		opts.Offset = int(req.Msg.Options.Offset)
	}
	tpls, err := h.service.ListTemplates(ctx, operatorID, opts)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.ListTemplatesResponse{
		Templates: mappers.TemplatesToProto(tpls),
	}), nil
}

func (h *TemplateHandler) UpdateTemplate(ctx context.Context, req *connect.Request[pb.UpdateTemplateRequest]) (*connect.Response[pb.UpdateTemplateResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tpl, err := h.factory.TemplateRepository().GetByID(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permService.CanManageTemplate(ctx, requestingUser, tpl.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	serviceReq := services.UpdateTemplateRequest{
		ChangeNote:      req.Msg.ChangeNote,
		CreatedByUserID: &requestingUser.ID,
	}
	if req.Msg.Description != nil {
		s := *req.Msg.Description
		serviceReq.Description = &s
	}
	if req.Msg.Permissions != nil || req.Msg.ResponsePermission != nil {
		serviceReq.PermissionsProvided = true
		if req.Msg.Permissions != nil {
			pa, pd, sa, sd := mappers.ProtoToUserPermissions(req.Msg.Permissions)
			serviceReq.PubAllow = pa
			serviceReq.PubDeny = pd
			serviceReq.SubAllow = sa
			serviceReq.SubDeny = sd
		}
		if req.Msg.ResponsePermission != nil {
			max, exp := mappers.ProtoToResponsePermission(req.Msg.ResponsePermission)
			serviceReq.ResponseMaxMsgs = &max
			ttl := time.Duration(exp)
			serviceReq.ResponseTTL = &ttl
		}
	}

	updatedTpl, newVer, err := h.service.UpdateTemplate(ctx, id, serviceReq)
	if err != nil {
		return nil, mapTemplateError(err)
	}
	return connect.NewResponse(&pb.UpdateTemplateResponse{
		Template: mappers.TemplateToProto(updatedTpl),
		Version:  mappers.TemplateVersionToProto(newVer),
	}), nil
}

func (h *TemplateHandler) DeleteTemplate(ctx context.Context, req *connect.Request[pb.DeleteTemplateRequest]) (*connect.Response[pb.DeleteTemplateResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tpl, err := h.factory.TemplateRepository().GetByID(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permService.CanManageTemplate(ctx, requestingUser, tpl.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if err := h.service.DeleteTemplate(ctx, id); err != nil {
		return nil, mapTemplateError(err)
	}
	return connect.NewResponse(&pb.DeleteTemplateResponse{}), nil
}

func (h *TemplateHandler) ListTemplateVersions(ctx context.Context, req *connect.Request[pb.ListTemplateVersionsRequest]) (*connect.Response[pb.ListTemplateVersionsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.TemplateId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tpl, err := h.factory.TemplateRepository().GetByID(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permService.CanReadTemplate(ctx, requestingUser, tpl.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	vers, err := h.service.ListTemplateVersions(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&pb.ListTemplateVersionsResponse{
		Versions: mappers.TemplateVersionsToProto(vers),
	}), nil
}

func (h *TemplateHandler) ListTemplateDependents(ctx context.Context, req *connect.Request[pb.ListTemplateDependentsRequest]) (*connect.Response[pb.ListTemplateDependentsResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := mappers.ParseUUID(req.Msg.TemplateId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tpl, err := h.factory.TemplateRepository().GetByID(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permService.CanReadTemplate(ctx, requestingUser, tpl.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	ssks, err := h.service.ListDependents(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	// Denormalize for the UI. One per-SSK account lookup is fine — the
	// dependent list is bounded by accounts-per-operator in practice;
	// when that grows we can switch to a JOIN.
	deps := make([]*pb.ScopedSigningKeyRef, 0, len(ssks))
	for _, ssk := range ssks {
		acct, err := h.factory.AccountRepository().GetByID(ctx, ssk.AccountID)
		if err != nil {
			return nil, repoErrToConnect(err)
		}
		ver := int32(0)
		if ssk.TemplateVersion != nil {
			ver = int32(*ssk.TemplateVersion)
		}
		deps = append(deps, &pb.ScopedSigningKeyRef{
			ScopedSigningKeyId:   mappers.UUIDToString(ssk.ID),
			AccountId:            mappers.UUIDToString(ssk.AccountID),
			AccountName:          acct.Name,
			ScopedSigningKeyName: ssk.Name,
			PinnedVersion:        ver,
			Drifted:              ssk.TemplateDrifted,
		})
	}
	return connect.NewResponse(&pb.ListTemplateDependentsResponse{Dependents: deps}), nil
}

func (h *TemplateHandler) ApplyTemplateToScopedKey(ctx context.Context, req *connect.Request[pb.ApplyTemplateToScopedKeyRequest]) (*connect.Response[pb.ApplyTemplateToScopedKeyResponse], error) {
	requestingUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	sskID, err := mappers.ParseUUID(req.Msg.ScopedSigningKeyId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	existing, err := h.factory.ScopedSigningKeyRepository().GetByID(ctx, sskID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permService.CanManageScopedKeys(ctx, requestingUser, existing.AccountID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	updated, err := h.sskService.BumpScopedKeyTemplate(ctx, sskID, int(req.Msg.VersionNumber))
	if err != nil {
		return nil, mapTemplateError(err)
	}
	return connect.NewResponse(&pb.ApplyTemplateToScopedKeyResponse{
		Key: mappers.ScopedSigningKeyToProto(updated),
	}), nil
}

// mapTemplateError translates TemplateService sentinels to gRPC codes
// before falling through to the generic repo-error mapper. Without this,
// reserved-name violations would surface as Internal; dependents-blocked
// deletes would surface as Unknown.
func mapTemplateError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, services.ErrTemplateNameReserved):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, services.ErrTemplateHasDependents):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, services.ErrTemplateVersionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, services.ErrTemplateRefForeignOperator):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, services.ErrScopedKeyNotTemplated):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return repoErrToConnect(err)
}

// Ensure the handler imports remain useful for future reference even
// when stubs are added. Kept here so an `unused` lint sweep doesn't
// strip the entities import (used indirectly by the imported services
// package via re-export).
var _ = entities.RoleAdmin
