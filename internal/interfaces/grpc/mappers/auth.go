package mappers

import (
	"github.com/thomas-maurice/nis/internal/domain/entities"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// APIUserToProto converts domain APIUser to protobuf APIUser
func APIUserToProto(user *entities.APIUser) *pb.APIUser {
	if user == nil {
		return nil
	}
	// Convert Role to permissions list
	// For now, permissions list contains just the role name
	// The actual permission enforcement is done via Casbin
	permissions := []string{string(user.Role)}

	pbUser := &pb.APIUser{
		Id:          UUIDToString(user.ID),
		Username:    user.Username,
		Permissions: permissions,
		CreatedAt:   timestamppb.New(user.CreatedAt),
		UpdatedAt:   timestamppb.New(user.UpdatedAt),
	}

	// Add optional operator_id and account_id
	if user.OperatorID != nil {
		operatorID := UUIDToString(*user.OperatorID)
		pbUser.OperatorId = &operatorID
	}
	if user.AccountID != nil {
		accountID := UUIDToString(*user.AccountID)
		pbUser.AccountId = &accountID
	}

	return pbUser
}

// ProtoToAPIUserRole converts protobuf permissions to APIUserRole
// Takes the first permission as the role
func ProtoToAPIUserRole(permissions []string) entities.APIUserRole {
	if len(permissions) == 0 {
		return entities.APIUserRole("")
	}
	return entities.APIUserRole(permissions[0])
}

// APIUsersToProto converts slice of domain APIUsers to protobuf APIUsers
func APIUsersToProto(users []*entities.APIUser) []*pb.APIUser {
	result := make([]*pb.APIUser, len(users))
	for i, user := range users {
		result[i] = APIUserToProto(user)
	}
	return result
}
