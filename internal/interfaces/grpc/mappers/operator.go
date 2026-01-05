package mappers

import (
	"github.com/thomas-maurice/nis/internal/domain/entities"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OperatorToProto converts domain Operator to protobuf Operator
func OperatorToProto(op *entities.Operator) *pb.Operator {
	if op == nil {
		return nil
	}
	return &pb.Operator{
		Id:                   UUIDToString(op.ID),
		Name:                 op.Name,
		Description:          op.Description,
		PublicKey:            op.PublicKey,
		Jwt:                  op.JWT,
		SystemAccountPubKey:  op.SystemAccountPubKey,
		CreatedAt:            timestamppb.New(op.CreatedAt),
		UpdatedAt:            timestamppb.New(op.UpdatedAt),
	}
}

// OperatorsToProto converts slice of domain Operators to protobuf Operators
func OperatorsToProto(ops []*entities.Operator) []*pb.Operator {
	result := make([]*pb.Operator, len(ops))
	for i, op := range ops {
		result[i] = OperatorToProto(op)
	}
	return result
}
