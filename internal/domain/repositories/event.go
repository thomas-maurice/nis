package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// EventFilter filters ListEvents queries.
type EventFilter struct {
	Types        []string   // if non-empty, restrict to these event types
	ResourceType string     // empty = any
	ResourceID   string     // empty = any
	OperatorID   *uuid.UUID // nil = any operator (or NULL operator events)
	AccountID    *uuid.UUID
	Since        *time.Time
	Until        *time.Time
	Limit        int    // <=0 → service-default
	Cursor       string // opaque; pass NextCursor from previous response
}

// EventListResult carries a page of events plus the cursor for the next page.
type EventListResult struct {
	Events     []*entities.Event
	NextCursor string // empty when no more pages
}

type EventRepository interface {
	// Create inserts a new event. ID and OccurredAt must already be set on the event.
	Create(ctx context.Context, e *entities.Event) error

	// GetByID retrieves an event by ID.
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Event, error)

	// List returns a filtered page of events ordered by occurred_at DESC, id DESC.
	List(ctx context.Context, filter EventFilter) (*EventListResult, error)

	// DeleteOlderThan removes events with occurred_at < cutoff. Returns count deleted.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
