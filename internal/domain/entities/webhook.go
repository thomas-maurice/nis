package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DeliveryStatus string

const (
	DeliveryStatusPending    DeliveryStatus = "pending"
	DeliveryStatusSucceeded  DeliveryStatus = "succeeded"
	DeliveryStatusFailed     DeliveryStatus = "failed"      // transient — will retry
	DeliveryStatusDeadLetter DeliveryStatus = "dead_letter" // permanent — max attempts hit
)

// EventTypeWildcard is the special wildcard for EventTypes meaning "all event types"
const EventTypeWildcard = "*"

// WebhookSubscription is an HTTP receiver configured to receive events for one operator.
type WebhookSubscription struct {
	ID              uuid.UUID
	OperatorID      uuid.UUID
	Name            string
	Description     string
	URL             string
	EncryptedSecret string   // already-encrypted storage ref; the plaintext HMAC key never lives here
	EventTypes      []string // marshalled to JSON in DB
	Enabled         bool
	DisabledReason  string // empty when healthy
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MatchesEventType reports whether this subscription should receive an event of the given type.
// Returns false if subscription is disabled.
func (s *WebhookSubscription) MatchesEventType(eventType string) bool {
	if !s.Enabled {
		return false
	}
	for _, t := range s.EventTypes {
		if t == EventTypeWildcard || t == eventType {
			return true
		}
	}
	return false
}

// WebhookDelivery is a single attempt-tracker for delivering one event to one subscription.
type WebhookDelivery struct {
	ID               uuid.UUID
	SubscriptionID   uuid.UUID
	EventID          uuid.UUID
	Attempt          int
	Status           DeliveryStatus
	NextAttemptAt    time.Time
	LastError        string
	LastResponseCode int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

// EventTypesJSON encodes a slice of event type strings to JSON for storage.
func EventTypesJSON(types []string) (string, error) {
	b, err := json.Marshal(types)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseEventTypesJSON decodes a JSON-encoded event type list from storage.
func ParseEventTypesJSON(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}
