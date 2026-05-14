package handlers

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

type WebhookHandler struct {
	svc     *services.WebhookService
	permSvc *services.PermissionService
}

func NewWebhookHandler(svc *services.WebhookService, permSvc *services.PermissionService) *WebhookHandler {
	return &WebhookHandler{svc: svc, permSvc: permSvc}
}

func containsWildcard(types []string) bool {
	for _, t := range types {
		if t == entities.EventTypeWildcard {
			return true
		}
	}
	return false
}

func (h *WebhookHandler) CreateWebhookSubscription(ctx context.Context, req *connect.Request[nisv1.CreateWebhookSubscriptionRequest]) (*connect.Response[nisv1.CreateWebhookSubscriptionResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := uuid.Parse(req.Msg.GetOperatorId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanCreateWebhookSubscription(ctx, apiUser, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if apiUser.Role != entities.RoleAdmin && containsWildcard(req.Msg.GetEventTypes()) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("operator-admins must enumerate event types; wildcard is admin-only"))
	}
	sub, secret, err := h.svc.CreateSubscription(ctx, services.CreateWebhookSubscriptionRequest{
		OperatorID:  operatorID,
		Name:        req.Msg.GetName(),
		Description: req.Msg.GetDescription(),
		URL:         req.Msg.GetUrl(),
		EventTypes:  req.Msg.GetEventTypes(),
	})
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.CreateWebhookSubscriptionResponse{
		Subscription: mappers.WebhookSubscriptionToProto(sub),
		Secret:       secret,
	}), nil
}

func (h *WebhookHandler) GetWebhookSubscription(ctx context.Context, req *connect.Request[nisv1.GetWebhookSubscriptionRequest]) (*connect.Response[nisv1.GetWebhookSubscriptionResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sub, err := h.svc.GetSubscription(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadWebhookSubscription(ctx, apiUser, sub.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(&nisv1.GetWebhookSubscriptionResponse{
		Subscription: mappers.WebhookSubscriptionToProto(sub),
	}), nil
}

func (h *WebhookHandler) ListWebhookSubscriptions(ctx context.Context, req *connect.Request[nisv1.ListWebhookSubscriptionsRequest]) (*connect.Response[nisv1.ListWebhookSubscriptionsResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}

	filter := repositories.WebhookSubscriptionFilter{}

	switch apiUser.Role {
	case entities.RoleAdmin:
		if opStr := req.Msg.GetOperatorId(); opStr != "" {
			opID, err := uuid.Parse(opStr)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
			filter.OperatorID = &opID
		}
	case entities.RoleOperatorAdmin:
		if apiUser.OperatorID == nil {
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("operator-admin has no operator assigned"))
		}
		// Always scope to own operator, ignore any operator_id the client sent.
		filter.OperatorID = apiUser.OperatorID
	default:
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("insufficient role to list webhook subscriptions"))
	}

	if opts := req.Msg.GetOptions(); opts != nil {
		filter.Limit = int(opts.GetLimit())
		filter.Offset = int(opts.GetOffset())
	}

	subs, err := h.svc.ListSubscriptions(ctx, filter)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	proto := make([]*nisv1.WebhookSubscription, 0, len(subs))
	for _, s := range subs {
		proto = append(proto, mappers.WebhookSubscriptionToProto(s))
	}
	return connect.NewResponse(&nisv1.ListWebhookSubscriptionsResponse{
		Subscriptions: proto,
	}), nil
}

func (h *WebhookHandler) UpdateWebhookSubscription(ctx context.Context, req *connect.Request[nisv1.UpdateWebhookSubscriptionRequest]) (*connect.Response[nisv1.UpdateWebhookSubscriptionResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sub, err := h.svc.GetSubscription(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanUpdateWebhookSubscription(ctx, apiUser, sub.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	updateReq := services.UpdateWebhookSubscriptionRequest{
		EventTypes: req.Msg.GetEventTypes(), // nil slice when not set = leave unchanged
	}

	// UpdateWebhookSubscriptionRequest uses proto3 optional (pointer fields).
	if req.Msg.Name != nil {
		updateReq.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		updateReq.Description = req.Msg.Description
	}
	if req.Msg.Url != nil {
		updateReq.URL = req.Msg.Url
	}
	if req.Msg.Enabled != nil {
		updateReq.Enabled = req.Msg.Enabled
	}

	if apiUser.Role != entities.RoleAdmin && containsWildcard(updateReq.EventTypes) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("operator-admins must enumerate event types; wildcard is admin-only"))
	}

	updated, err := h.svc.UpdateSubscription(ctx, id, updateReq)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.UpdateWebhookSubscriptionResponse{
		Subscription: mappers.WebhookSubscriptionToProto(updated),
	}), nil
}

func (h *WebhookHandler) DeleteWebhookSubscription(ctx context.Context, req *connect.Request[nisv1.DeleteWebhookSubscriptionRequest]) (*connect.Response[nisv1.DeleteWebhookSubscriptionResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sub, err := h.svc.GetSubscription(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanDeleteWebhookSubscription(ctx, apiUser, sub.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if err := h.svc.DeleteSubscription(ctx, id); err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.DeleteWebhookSubscriptionResponse{}), nil
}

func (h *WebhookHandler) TestWebhookSubscription(ctx context.Context, req *connect.Request[nisv1.TestWebhookSubscriptionRequest]) (*connect.Response[nisv1.TestWebhookSubscriptionResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sub, err := h.svc.GetSubscription(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadWebhookSubscription(ctx, apiUser, sub.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	deliveryID, err := h.svc.TestSubscription(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.TestWebhookSubscriptionResponse{
		DeliveryId: deliveryID.String(),
	}), nil
}

func (h *WebhookHandler) ListWebhookDeliveries(ctx context.Context, req *connect.Request[nisv1.ListWebhookDeliveriesRequest]) (*connect.Response[nisv1.ListWebhookDeliveriesResponse], error) {
	apiUser, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	subID, err := uuid.Parse(req.Msg.GetSubscriptionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sub, err := h.svc.GetSubscription(ctx, subID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadWebhookSubscription(ctx, apiUser, sub.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	filter := repositories.WebhookDeliveryFilter{
		SubscriptionID: &subID,
	}
	if s := req.Msg.GetStatus(); s != "" {
		status := entities.DeliveryStatus(s)
		filter.Status = &status
	}
	if opts := req.Msg.GetOptions(); opts != nil {
		filter.Limit = int(opts.GetLimit())
		filter.Offset = int(opts.GetOffset())
	}

	deliveries, err := h.svc.ListDeliveries(ctx, filter)
	if err != nil {
		return nil, repoErrToConnect(err)
	}

	proto := make([]*nisv1.WebhookDelivery, 0, len(deliveries))
	for _, d := range deliveries {
		proto = append(proto, mappers.WebhookDeliveryToProto(d))
	}
	return connect.NewResponse(&nisv1.ListWebhookDeliveriesResponse{
		Deliveries: proto,
	}), nil
}
