package mappers

import (
	"encoding/json"
	"time"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EventToProto converts a domain Event to its protobuf representation.
func EventToProto(e *entities.Event) *nisv1.Event {
	if e == nil {
		return nil
	}

	actorID := ""
	if e.ActorID != nil {
		actorID = e.ActorID.String()
	}

	operatorID := ""
	if e.OperatorID != nil {
		operatorID = e.OperatorID.String()
	}

	accountID := ""
	if e.AccountID != nil {
		accountID = e.AccountID.String()
	}

	payloadJSON := ""
	if len(e.Payload) > 0 {
		payloadJSON = string(e.Payload)
	}

	return &nisv1.Event{
		Id:           e.ID.String(),
		OccurredAt:   timestamppb.New(e.OccurredAt),
		Type:         e.Type,
		ActorType:    string(e.ActorType),
		ActorId:      actorID,
		OperatorId:   operatorID,
		AccountId:    accountID,
		ResourceType: e.ResourceType,
		ResourceId:   e.ResourceID,
		PayloadJson:  payloadJSON,
	}
}

// EventFilterFromProto converts a proto EventFilter to the domain repositories.EventFilter.
func EventFilterFromProto(f *nisv1.EventFilter) repositories.EventFilter {
	if f == nil {
		return repositories.EventFilter{}
	}

	out := repositories.EventFilter{
		Types:        f.Types,
		ResourceType: f.ResourceType,
		ResourceID:   f.ResourceId,
		Limit:        int(f.Limit),
		Cursor:       f.Cursor,
	}

	if f.OperatorId != "" {
		if id, err := ParseUUID(f.OperatorId); err == nil {
			out.OperatorID = &id
		}
	}

	if f.AccountId != "" {
		if id, err := ParseUUID(f.AccountId); err == nil {
			out.AccountID = &id
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

// EventPayloadPretty returns the payload as pretty-printed JSON if possible,
// or the raw string otherwise. Used by nisctl get output.
func EventPayloadPretty(payloadJSON string) string {
	if payloadJSON == "" {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &v); err != nil {
		return payloadJSON
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return payloadJSON
	}
	return string(b)
}

// FormatSinceFlag converts a duration string like "24h" into a *time.Time for
// EventFilter.Since. Returns nil if dur is zero.
func FormatSinceFlag(dur time.Duration) *time.Time {
	if dur == 0 {
		return nil
	}
	t := time.Now().Add(-dur)
	return &t
}
