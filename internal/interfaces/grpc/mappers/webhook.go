package mappers

import (
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WebhookSubscriptionToProto converts a domain WebhookSubscription to proto.
// The HMAC secret is never included — it is only returned once at Create time.
func WebhookSubscriptionToProto(s *entities.WebhookSubscription) *nisv1.WebhookSubscription {
	if s == nil {
		return nil
	}
	return &nisv1.WebhookSubscription{
		Id:             s.ID.String(),
		OperatorId:     s.OperatorID.String(),
		Name:           s.Name,
		Description:    s.Description,
		Url:            s.URL,
		EventTypes:     s.EventTypes,
		Enabled:        s.Enabled,
		DisabledReason: s.DisabledReason,
		CreatedAt:      timestamppb.New(s.CreatedAt),
		UpdatedAt:      timestamppb.New(s.UpdatedAt),
	}
}

// WebhookDeliveryToProto converts a domain WebhookDelivery to proto.
func WebhookDeliveryToProto(d *entities.WebhookDelivery) *nisv1.WebhookDelivery {
	if d == nil {
		return nil
	}
	out := &nisv1.WebhookDelivery{
		Id:               d.ID.String(),
		SubscriptionId:   d.SubscriptionID.String(),
		EventId:          d.EventID.String(),
		Attempt:          int32(d.Attempt),
		Status:           string(d.Status),
		NextAttemptAt:    timestamppb.New(d.NextAttemptAt),
		LastError:        d.LastError,
		LastResponseCode: int32(d.LastResponseCode),
		CreatedAt:        timestamppb.New(d.CreatedAt),
		UpdatedAt:        timestamppb.New(d.UpdatedAt),
	}
	if d.CompletedAt != nil {
		out.CompletedAt = timestamppb.New(*d.CompletedAt)
	}
	return out
}
