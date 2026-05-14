package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

type WebhookService struct {
	factory   persistence.RepositoryFactory
	encryptor encryption.Encryptor
}

func NewWebhookService(factory persistence.RepositoryFactory, encryptor encryption.Encryptor) *WebhookService {
	return &WebhookService{factory: factory, encryptor: encryptor}
}

type CreateWebhookSubscriptionRequest struct {
	OperatorID  uuid.UUID
	Name        string
	Description string
	URL         string
	EventTypes  []string
}

// CreateSubscription generates a 32-byte HMAC secret, encrypts it, persists the
// subscription, and returns the subscription plus the plaintext secret. The
// plaintext secret is returned exactly once and is never recoverable after this call.
func (s *WebhookService) CreateSubscription(ctx context.Context, req CreateWebhookSubscriptionRequest) (*entities.WebhookSubscription, string, error) {
	if req.Name == "" || req.URL == "" {
		return nil, "", fmt.Errorf("name and url are required")
	}
	if len(req.EventTypes) == 0 {
		return nil, "", fmt.Errorf("at least one event_type is required")
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, "", fmt.Errorf("generate secret: %w", err)
	}
	secretHex := hex.EncodeToString(secretBytes)
	encSecret, err := s.encryptor.Encrypt(ctx, []byte(secretHex))
	if err != nil {
		return nil, "", fmt.Errorf("encrypt secret: %w", err)
	}

	sub := &entities.WebhookSubscription{
		ID:              uuid.New(),
		OperatorID:      req.OperatorID,
		Name:            req.Name,
		Description:     req.Description,
		URL:             req.URL,
		EncryptedSecret: encSecret,
		EventTypes:      req.EventTypes,
		Enabled:         true,
	}
	if err := s.factory.WebhookSubscriptionRepository().Create(ctx, sub); err != nil {
		return nil, "", fmt.Errorf("create subscription: %w", err)
	}
	return sub, secretHex, nil
}

func (s *WebhookService) GetSubscription(ctx context.Context, id uuid.UUID) (*entities.WebhookSubscription, error) {
	return s.factory.WebhookSubscriptionRepository().GetByID(ctx, id)
}

func (s *WebhookService) ListSubscriptions(ctx context.Context, filter repositories.WebhookSubscriptionFilter) ([]*entities.WebhookSubscription, error) {
	return s.factory.WebhookSubscriptionRepository().List(ctx, filter)
}

type UpdateWebhookSubscriptionRequest struct {
	Name        *string
	Description *string
	URL         *string
	EventTypes  []string // nil = leave unchanged; non-nil = replace
	Enabled     *bool
}

func (s *WebhookService) UpdateSubscription(ctx context.Context, id uuid.UUID, req UpdateWebhookSubscriptionRequest) (*entities.WebhookSubscription, error) {
	sub, err := s.factory.WebhookSubscriptionRepository().GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		sub.Name = *req.Name
	}
	if req.Description != nil {
		sub.Description = *req.Description
	}
	if req.URL != nil {
		sub.URL = *req.URL
	}
	if req.EventTypes != nil {
		sub.EventTypes = req.EventTypes
	}
	if req.Enabled != nil {
		sub.Enabled = *req.Enabled
		if *req.Enabled {
			sub.DisabledReason = ""
		}
	}
	if err := s.factory.WebhookSubscriptionRepository().Update(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *WebhookService) DeleteSubscription(ctx context.Context, id uuid.UUID) error {
	return s.factory.WebhookSubscriptionRepository().Delete(ctx, id)
}

// TestSubscription emits a webhook.test event scoped to the subscription's operator.
// The fanout in events.EmitTx creates a delivery row for any matching subscription
// (including this one if it has "webhook.test" or "*" in its event types).
// Returns the delivery ID created for this subscription, or an error if the subscription
// is disabled or the fanout produced no delivery row for it.
func (s *WebhookService) TestSubscription(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	sub, err := s.factory.WebhookSubscriptionRepository().GetByID(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if !sub.Enabled {
		return uuid.Nil, fmt.Errorf("subscription is disabled")
	}

	var deliveryID uuid.UUID
	err = s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeWebhookTest,
			OperatorID:   &sub.OperatorID,
			ResourceType: "webhook_subscription",
			ResourceID:   sub.ID.String(),
			Payload: map[string]any{
				"subscription_id": sub.ID.String(),
				"message":         "Test delivery from NIS",
			},
		}); err != nil {
			return err
		}

		deliveries, err := tx.WebhookDeliveryRepository().List(ctx, repositories.WebhookDeliveryFilter{
			SubscriptionID: &sub.ID,
			Limit:          1,
		})
		if err != nil {
			return err
		}
		if len(deliveries) == 0 {
			return fmt.Errorf("test event emitted but no delivery row created — subscription event_types may not include %q or %q", entities.EventTypeWebhookTest, entities.EventTypeWildcard)
		}
		deliveryID = deliveries[0].ID
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return deliveryID, nil
}

func (s *WebhookService) ListDeliveries(ctx context.Context, filter repositories.WebhookDeliveryFilter) ([]*entities.WebhookDelivery, error) {
	return s.factory.WebhookDeliveryRepository().List(ctx, filter)
}
