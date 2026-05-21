package handlers

import (
	"context"
	"errors"
	"io"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BackupHandler is the RPC surface for the P12 scheduled-backup feature.
// Per-row permission narrowing goes through PermissionService.CanManageBackup
// / CanReadBackup; Casbin gates the coarse (role, resource, action) layer.
type BackupHandler struct {
	svc      *services.BackupService
	opSvc    *services.OperatorService
	permSvc  *services.PermissionService
}

func NewBackupHandler(svc *services.BackupService, opSvc *services.OperatorService, permSvc *services.PermissionService) *BackupHandler {
	return &BackupHandler{svc: svc, opSvc: opSvc, permSvc: permSvc}
}

// backupServiceGuard short-circuits every RPC when the service is nil or
// the global flag is off — without it, BackupService.* methods would
// return ErrBackupsDisabled with a generic Internal mapping, which is
// less helpful than a typed FailedPrecondition.
func (h *BackupHandler) backupServiceGuard() error {
	if h.svc == nil || !h.svc.Enabled() {
		return connect.NewError(connect.CodeFailedPrecondition, services.ErrBackupsDisabled)
	}
	return nil
}

func (h *BackupHandler) UpdateOperatorBackupSettings(ctx context.Context, req *connect.Request[nisv1.UpdateOperatorBackupSettingsRequest]) (*connect.Response[nisv1.UpdateOperatorBackupSettingsResponse], error) {
	if err := h.backupServiceGuard(); err != nil {
		return nil, err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := uuid.Parse(req.Msg.GetOperatorId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanManageBackup(ctx, user, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	settings := services.BackupSettings{}
	if req.Msg.Enabled != nil {
		v := req.Msg.GetEnabled()
		settings.Enabled = &v
	}
	if req.Msg.IntervalSeconds != nil {
		d := time.Duration(req.Msg.GetIntervalSeconds()) * time.Second
		settings.Interval = &d
	}
	if req.Msg.RetentionCount != nil {
		v := int(req.Msg.GetRetentionCount())
		settings.RetentionCount = &v
	}
	op, err := h.svc.UpdateSettings(ctx, operatorID, settings)
	if err != nil {
		if errors.Is(err, services.ErrBackupIntervalTooSmall) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.UpdateOperatorBackupSettingsResponse{
		Settings: backupSettingsToProto(op),
	}), nil
}

func (h *BackupHandler) GetOperatorBackupSettings(ctx context.Context, req *connect.Request[nisv1.GetOperatorBackupSettingsRequest]) (*connect.Response[nisv1.GetOperatorBackupSettingsResponse], error) {
	if err := h.backupServiceGuard(); err != nil {
		return nil, err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := uuid.Parse(req.Msg.GetOperatorId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanReadBackup(ctx, user, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	op, err := h.opSvc.GetOperator(ctx, operatorID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.GetOperatorBackupSettingsResponse{
		Settings: backupSettingsToProto(op),
	}), nil
}

func (h *BackupHandler) RunOperatorBackup(ctx context.Context, req *connect.Request[nisv1.RunOperatorBackupRequest]) (*connect.Response[nisv1.RunOperatorBackupResponse], error) {
	if err := h.backupServiceGuard(); err != nil {
		return nil, err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := uuid.Parse(req.Msg.GetOperatorId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanManageBackup(ctx, user, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	backup, err := h.svc.RunBackup(ctx, operatorID, entities.BackupTriggerManual)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.RunOperatorBackupResponse{
		Backup: operatorBackupToProto(backup),
	}), nil
}

func (h *BackupHandler) ListOperatorBackups(ctx context.Context, req *connect.Request[nisv1.ListOperatorBackupsRequest]) (*connect.Response[nisv1.ListOperatorBackupsResponse], error) {
	if err := h.backupServiceGuard(); err != nil {
		return nil, err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	operatorID, err := uuid.Parse(req.Msg.GetOperatorId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.permSvc.CanReadBackup(ctx, user, operatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	rows, err := h.svc.ListBackups(ctx, operatorID)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	out := &nisv1.ListOperatorBackupsResponse{Backups: make([]*nisv1.OperatorBackup, len(rows))}
	for i, r := range rows {
		out.Backups[i] = operatorBackupToProto(r)
	}
	return connect.NewResponse(out), nil
}

func (h *BackupHandler) GetBackup(ctx context.Context, req *connect.Request[nisv1.GetBackupRequest]) (*connect.Response[nisv1.GetBackupResponse], error) {
	if err := h.backupServiceGuard(); err != nil {
		return nil, err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	row, err := h.svc.GetBackup(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadBackup(ctx, user, row.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(&nisv1.GetBackupResponse{Backup: operatorBackupToProto(row)}), nil
}

func (h *BackupHandler) DownloadBackup(ctx context.Context, req *connect.Request[nisv1.DownloadBackupRequest], stream *connect.ServerStream[nisv1.DownloadBackupResponse]) error {
	if err := h.backupServiceGuard(); err != nil {
		return err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	row, err := h.svc.GetBackup(ctx, id)
	if err != nil {
		return repoErrToConnect(err)
	}
	if err := h.permSvc.CanReadBackup(ctx, user, row.OperatorID); err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	rc, _, err := h.svc.DownloadBackup(ctx, id)
	if err != nil {
		return repoErrToConnect(err)
	}
	defer func() { _ = rc.Close() }()

	if err := stream.Send(&nisv1.DownloadBackupResponse{
		Payload: &nisv1.DownloadBackupResponse_Metadata{Metadata: operatorBackupToProto(row)},
	}); err != nil {
		return err
	}
	// 256 KiB chunks — balance between per-chunk Send overhead and memory
	// pressure for a worst-case multi-MB operator backup.
	buf := make([]byte, 256*1024)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&nisv1.DownloadBackupResponse{
				Payload: &nisv1.DownloadBackupResponse_Chunk{Chunk: chunk},
			}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return connect.NewError(connect.CodeInternal, readErr)
		}
	}
}

func (h *BackupHandler) DeleteBackup(ctx context.Context, req *connect.Request[nisv1.DeleteBackupRequest]) (*connect.Response[nisv1.DeleteBackupResponse], error) {
	if err := h.backupServiceGuard(); err != nil {
		return nil, err
	}
	user, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	row, err := h.svc.GetBackup(ctx, id)
	if err != nil {
		return nil, repoErrToConnect(err)
	}
	if err := h.permSvc.CanManageBackup(ctx, user, row.OperatorID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	actor := user.ID
	if err := h.svc.DeleteBackup(ctx, id, &actor); err != nil {
		return nil, repoErrToConnect(err)
	}
	return connect.NewResponse(&nisv1.DeleteBackupResponse{}), nil
}

func backupSettingsToProto(op *entities.Operator) *nisv1.BackupSettings {
	s := &nisv1.BackupSettings{Enabled: op.BackupEnabled}
	if op.BackupInterval != nil {
		s.IntervalSeconds = int64(op.BackupInterval.Seconds())
	}
	if op.BackupRetention != nil {
		s.RetentionCount = int32(*op.BackupRetention)
	}
	if op.LastBackupAt != nil {
		s.LastBackupAt = timestamppb.New(*op.LastBackupAt)
	}
	return s
}

func operatorBackupToProto(b *entities.OperatorBackup) *nisv1.OperatorBackup {
	return &nisv1.OperatorBackup{
		Id:          b.ID.String(),
		OperatorId:  b.OperatorID.String(),
		ObjectKey:   b.ObjectKey,
		SizeBytes:   b.SizeBytes,
		Sha256:      b.Sha256,
		TriggerKind: string(b.TriggerKind),
		CreatedAt:   timestamppb.New(b.CreatedAt),
	}
}
