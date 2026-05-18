package mappers

import (
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// APITokenToProto converts a domain APIToken to its proto form. The plaintext
// is never carried on this message — it's only ever in the Create response.
func APITokenToProto(t *entities.APIToken) *nisv1.APIToken {
	if t == nil {
		return nil
	}
	out := &nisv1.APIToken{
		Id:          t.ID.String(),
		Name:        t.Name,
		Prefix:      t.Prefix,
		Description: t.Description,
		Role:        string(t.Role),
		CreatedAt:   timestamppb.New(t.CreatedAt),
		UpdatedAt:   timestamppb.New(t.UpdatedAt),
	}
	if t.CreatedByUserID != nil {
		out.CreatedByUserId = t.CreatedByUserID.String()
	}
	if t.OperatorID != nil {
		out.OperatorId = t.OperatorID.String()
	}
	if t.AccountID != nil {
		out.AccountId = t.AccountID.String()
	}
	if t.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*t.ExpiresAt)
	}
	if t.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*t.LastUsedAt)
	}
	if t.RevokedAt != nil {
		out.RevokedAt = timestamppb.New(*t.RevokedAt)
	}
	return out
}
