package handlers

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

type EventHandler struct {
	svc *services.EventService
}

func NewEventHandler(svc *services.EventService) *EventHandler {
	return &EventHandler{svc: svc}
}

func (h *EventHandler) ListEvents(ctx context.Context, req *connect.Request[nisv1.ListEventsRequest]) (*connect.Response[nisv1.ListEventsResponse], error) {
	filter := mappers.EventFilterFromProto(req.Msg.GetFilter())
	res, err := h.svc.ListEvents(ctx, filter)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	out := &nisv1.ListEventsResponse{
		Events:     make([]*nisv1.Event, len(res.Events)),
		NextCursor: res.NextCursor,
	}
	for i, e := range res.Events {
		out.Events[i] = mappers.EventToProto(e)
	}
	return connect.NewResponse(out), nil
}

func (h *EventHandler) GetEvent(ctx context.Context, req *connect.Request[nisv1.GetEventRequest]) (*connect.Response[nisv1.GetEventResponse], error) {
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	evt, err := h.svc.GetEvent(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.GetEventResponse{Event: mappers.EventToProto(evt)}), nil
}
