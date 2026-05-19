package mappers

import (
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AccountToProto converts domain Account to protobuf Account
func AccountToProto(acc *entities.Account) *pb.Account {
	if acc == nil {
		return nil
	}
	return &pb.Account{
		Id:          UUIDToString(acc.ID),
		OperatorId:  UUIDToString(acc.OperatorID),
		Name:        acc.Name,
		Description: acc.Description,
		PublicKey:   acc.PublicKey,
		Jwt:         acc.JWT,
		JetstreamLimits: &pb.JetStreamLimits{
			Enabled:      acc.JetStreamEnabled,
			MaxMemory:    acc.JetStreamMaxMemory,
			MaxStorage:   acc.JetStreamMaxStorage,
			MaxStreams:   int32(acc.JetStreamMaxStreams),
			MaxConsumers: int32(acc.JetStreamMaxConsumers),
		},
		CreatedAt: timestamppb.New(acc.CreatedAt),
		UpdatedAt: timestamppb.New(acc.UpdatedAt),
	}
}

// AccountsToProto converts slice of domain Accounts to protobuf Accounts
func AccountsToProto(accs []*entities.Account) []*pb.Account {
	result := make([]*pb.Account, len(accs))
	for i, acc := range accs {
		result[i] = AccountToProto(acc)
	}
	return result
}

// ProtoToJetStreamLimits converts protobuf JetStreamLimits to domain fields
func ProtoToJetStreamLimits(limits *pb.JetStreamLimits) (bool, int64, int64, int64, int64) {
	if limits == nil {
		return false, 0, 0, 0, 0
	}
	return limits.Enabled,
		limits.MaxMemory,
		limits.MaxStorage,
		int64(limits.MaxStreams),
		int64(limits.MaxConsumers)
}

// AccountJWTRevocationViewToProto converts a service-level revocation view
// (entity + denormalised user fields) to its protobuf representation.
func AccountJWTRevocationViewToProto(v *services.AccountJWTRevocationView) *pb.AccountJWTRevocation {
	if v == nil || v.Revocation == nil {
		return nil
	}
	rev := v.Revocation
	out := &pb.AccountJWTRevocation{
		Id:               UUIDToString(rev.ID),
		AccountId:        UUIDToString(rev.AccountID),
		UserName:         v.UserName,
		UserPublicKey:    rev.UserPublicKey,
		RevokedAt:        timestamppb.New(rev.RevokedAt),
		JwtExp:           timestamppb.New(rev.JWTExp),
		Reason:           rev.Reason,
		UserStillFlagged: v.UserStillFlagged,
	}
	if rev.UserID != nil {
		out.UserId = UUIDToString(*rev.UserID)
	}
	return out
}

// AccountJWTRevocationViewsToProto maps a slice.
func AccountJWTRevocationViewsToProto(vs []*services.AccountJWTRevocationView) []*pb.AccountJWTRevocation {
	out := make([]*pb.AccountJWTRevocation, len(vs))
	for i, v := range vs {
		out[i] = AccountJWTRevocationViewToProto(v)
	}
	return out
}

// ClusterJetStreamUsageToProto converts a service-level probe result to its
// proto representation. The internal enum values are kept aligned with the
// proto enum order so this is a direct cast — if you reorder one, mirror it.
func ClusterJetStreamUsageToProto(u *services.ClusterJetStreamUsage) *pb.ClusterJetStreamUsage {
	if u == nil {
		return nil
	}
	out := &pb.ClusterJetStreamUsage{
		ClusterId:    UUIDToString(u.ClusterID),
		ClusterName:  u.ClusterName,
		Status:       pb.JetStreamProbeStatus(u.Status),
		ErrorMessage: u.ErrorMessage,
	}
	if u.Usage != nil {
		out.Usage = &pb.JetStreamUsage{
			MemoryUsed:      u.Usage.Memory,
			StorageUsed:     u.Usage.Storage,
			ReservedMemory:  u.Usage.ReservedMemory,
			ReservedStorage: u.Usage.ReservedStorage,
			Streams:         int32(u.Usage.Streams),
			Consumers:       int32(u.Usage.Consumers),
			ApiTotal:        u.Usage.APITotal,
			ApiErrors:       u.Usage.APIErrors,
		}
	}
	return out
}

// ClusterJetStreamUsagesToProto maps a slice.
func ClusterJetStreamUsagesToProto(us []*services.ClusterJetStreamUsage) []*pb.ClusterJetStreamUsage {
	out := make([]*pb.ClusterJetStreamUsage, len(us))
	for i, u := range us {
		out[i] = ClusterJetStreamUsageToProto(u)
	}
	return out
}
