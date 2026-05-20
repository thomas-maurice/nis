package mappers

import (
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// JobStatusToProto translates the entity enum to the proto enum.
func JobStatusToProto(s entities.JobStatus) nisv1.JobStatus {
	switch s {
	case entities.JobStatusPending:
		return nisv1.JobStatus_JOB_STATUS_PENDING
	case entities.JobStatusRunning:
		return nisv1.JobStatus_JOB_STATUS_RUNNING
	case entities.JobStatusSucceeded:
		return nisv1.JobStatus_JOB_STATUS_SUCCEEDED
	case entities.JobStatusFailed:
		return nisv1.JobStatus_JOB_STATUS_FAILED
	case entities.JobStatusDeadLettered:
		return nisv1.JobStatus_JOB_STATUS_DEAD_LETTERED
	case entities.JobStatusCancelled:
		return nisv1.JobStatus_JOB_STATUS_CANCELLED
	default:
		return nisv1.JobStatus_JOB_STATUS_UNSPECIFIED
	}
}

// JobStatusFromProto is the inverse. JOB_STATUS_UNSPECIFIED maps to "" so
// callers passing an unfiltered enum don't accidentally restrict the
// filter to nonsense.
func JobStatusFromProto(s nisv1.JobStatus) entities.JobStatus {
	switch s {
	case nisv1.JobStatus_JOB_STATUS_PENDING:
		return entities.JobStatusPending
	case nisv1.JobStatus_JOB_STATUS_RUNNING:
		return entities.JobStatusRunning
	case nisv1.JobStatus_JOB_STATUS_SUCCEEDED:
		return entities.JobStatusSucceeded
	case nisv1.JobStatus_JOB_STATUS_FAILED:
		return entities.JobStatusFailed
	case nisv1.JobStatus_JOB_STATUS_DEAD_LETTERED:
		return entities.JobStatusDeadLettered
	case nisv1.JobStatus_JOB_STATUS_CANCELLED:
		return entities.JobStatusCancelled
	default:
		return ""
	}
}

func JobToProto(j *entities.Job) *nisv1.Job {
	if j == nil {
		return nil
	}
	out := &nisv1.Job{
		Id:           j.ID.String(),
		Type:         j.Type,
		Payload:      string(j.Payload),
		Status:       JobStatusToProto(j.Status),
		ScheduledFor: timestamppb.New(j.ScheduledFor),
		LockedBy:     j.LockedBy,
		Attempts:     int32(j.Attempts),
		MaxAttempts:  int32(j.MaxAttempts),
		LastError:    j.LastError,
		DedupKey:     j.DedupKey,
		CreatedAt:    timestamppb.New(j.CreatedAt),
		UpdatedAt:    timestamppb.New(j.UpdatedAt),
	}
	if j.LockedUntil != nil {
		out.LockedUntil = timestamppb.New(*j.LockedUntil)
	}
	if j.StartedAt != nil {
		out.StartedAt = timestamppb.New(*j.StartedAt)
	}
	if j.CompletedAt != nil {
		out.CompletedAt = timestamppb.New(*j.CompletedAt)
	}
	return out
}

func JobFilterFromProto(f *nisv1.JobFilter) repositories.JobFilter {
	if f == nil {
		return repositories.JobFilter{}
	}
	out := repositories.JobFilter{
		Types:  f.Types,
		Limit:  int(f.Limit),
		Cursor: f.Cursor,
	}
	for _, s := range f.Statuses {
		es := JobStatusFromProto(s)
		if es != "" {
			out.Statuses = append(out.Statuses, es)
		}
	}
	if f.Since != nil && f.Since.IsValid() {
		t := f.Since.AsTime()
		out.Since = &t
	}
	if f.Until != nil && f.Until.IsValid() {
		t := f.Until.AsTime()
		out.Until = &t
	}
	return out
}
