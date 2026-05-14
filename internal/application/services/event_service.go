package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

type EventService struct {
	factory persistence.RepositoryFactory
}

func NewEventService(factory persistence.RepositoryFactory) *EventService {
	return &EventService{factory: factory}
}

func (s *EventService) ListEvents(ctx context.Context, filter repositories.EventFilter) (*repositories.EventListResult, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}
	res, err := s.factory.EventRepository().List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return res, nil
}

func (s *EventService) GetEvent(ctx context.Context, id uuid.UUID) (*entities.Event, error) {
	return s.factory.EventRepository().GetByID(ctx, id)
}
