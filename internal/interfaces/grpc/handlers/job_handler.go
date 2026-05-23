package handlers

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/mappers"
)

// JobHandler is the admin-only RPC surface for the A2 jobs substrate.
// Admin-only at the handler layer via the shared requireAdmin gate (util.go);
// the authz registry also restricts to admin (see RolePolicy in
// authz/registry.go). Per-row narrowing is intentionally not done — jobs are
// infrastructure, not tenant data. If a future job type carries operator_id
// in payload and warrants per-operator scoping, add it then.
type JobHandler struct {
	svc *services.JobService
}

func NewJobHandler(svc *services.JobService) *JobHandler {
	return &JobHandler{svc: svc}
}

func (h *JobHandler) ListJobs(ctx context.Context, req *connect.Request[nisv1.ListJobsRequest]) (*connect.Response[nisv1.ListJobsResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	filter := mappers.JobFilterFromProto(req.Msg.GetFilter())
	jobs, cursor, err := h.svc.ListJobs(ctx, filter)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	out := &nisv1.ListJobsResponse{
		Jobs:       make([]*nisv1.Job, len(jobs)),
		NextCursor: cursor,
	}
	for i, j := range jobs {
		out.Jobs[i] = mappers.JobToProto(j)
	}
	return connect.NewResponse(out), nil
}

func (h *JobHandler) GetJob(ctx context.Context, req *connect.Request[nisv1.GetJobRequest]) (*connect.Response[nisv1.GetJobResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	j, err := h.svc.GetJob(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.GetJobResponse{Job: mappers.JobToProto(j)}), nil
}

func (h *JobHandler) RetryJob(ctx context.Context, req *connect.Request[nisv1.RetryJobRequest]) (*connect.Response[nisv1.RetryJobResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	j, err := h.svc.RetryJob(ctx, id)
	if err != nil {
		return nil, mapJobStateErr(err)
	}
	return connect.NewResponse(&nisv1.RetryJobResponse{Job: mappers.JobToProto(j)}), nil
}

func (h *JobHandler) CancelJob(ctx context.Context, req *connect.Request[nisv1.CancelJobRequest]) (*connect.Response[nisv1.CancelJobResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	j, err := h.svc.CancelJob(ctx, id)
	if err != nil {
		return nil, mapJobStateErr(err)
	}
	return connect.NewResponse(&nisv1.CancelJobResponse{Job: mappers.JobToProto(j)}), nil
}
