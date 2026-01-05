package mappers

import (
	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UserToProto converts domain User to protobuf User
func UserToProto(user *entities.User) *pb.User {
	if user == nil {
		return nil
	}

	scopedKeyID := ""
	if user.ScopedSigningKeyID != nil {
		scopedKeyID = UUIDToString(*user.ScopedSigningKeyID)
	}

	return &pb.User{
		Id:                  UUIDToString(user.ID),
		AccountId:           UUIDToString(user.AccountID),
		Name:                user.Name,
		Description:         user.Description,
		PublicKey:           user.PublicKey,
		Jwt:                 user.JWT,
		ScopedSigningKeyId:  scopedKeyID,
		CreatedAt:           timestamppb.New(user.CreatedAt),
		UpdatedAt:           timestamppb.New(user.UpdatedAt),
	}
}

// UsersToProto converts slice of domain Users to protobuf Users
func UsersToProto(users []*entities.User) []*pb.User {
	result := make([]*pb.User, len(users))
	for i, user := range users {
		result[i] = UserToProto(user)
	}
	return result
}

// ProtoToScopedKeyID converts protobuf scoped key ID to UUID pointer
func ProtoToScopedKeyID(id string) (*uuid.UUID, error) {
	if id == "" {
		return nil, nil
	}
	parsed, err := ParseUUID(id)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
