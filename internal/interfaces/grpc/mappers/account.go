package mappers

import (
	"github.com/thomas-maurice/nis/internal/domain/entities"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
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
