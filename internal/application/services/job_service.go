package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// JobService is the read/admin-control surface over the jobs table.
// Distinct from JobRunner — runner executes work, service answers admin
// "show me + retry + cancel" queries.
//
// All Can* checks live in the handler layer; this service does no
// permission filtering of its own (admin sees everything).
type JobService struct {
	factory persistence.RepositoryFactory
}

func NewJobService(factory persistence.RepositoryFactory) *JobService {
	return &JobService{factory: factory}
}

func (s *JobService) ListJobs(ctx context.Context, filter repositories.JobFilter) ([]*entities.Job, string, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	jobs, cursor, err := s.factory.JobRepository().List(ctx, filter)
	if err != nil {
		return nil, "", fmt.Errorf("list jobs: %w", err)
	}
	return jobs, cursor, nil
}

func (s *JobService) GetJob(ctx context.Context, id uuid.UUID) (*entities.Job, error) {
	return s.factory.JobRepository().Get(ctx, id)
}

// RetryJob resets a failed / dead-lettered / cancelled row to pending and
// returns the refreshed entity. ErrJobInvalidStateTransition propagates
// when the row is currently pending or running (admin error — there's
// nothing to retry).
func (s *JobService) RetryJob(ctx context.Context, id uuid.UUID) (*entities.Job, error) {
	if err := s.factory.JobRepository().Retry(ctx, id, clock.Now()); err != nil {
		return nil, err
	}
	return s.factory.JobRepository().Get(ctx, id)
}

// CancelJob transitions a pending row to cancelled. ErrJobInvalidStateTransition
// when the row is already running, finished, or cancelled.
func (s *JobService) CancelJob(ctx context.Context, id uuid.UUID) (*entities.Job, error) {
	if err := s.factory.JobRepository().Cancel(ctx, id); err != nil {
		return nil, err
	}
	return s.factory.JobRepository().Get(ctx, id)
}
