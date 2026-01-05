package mappers

import (
	"github.com/thomas-maurice/nis/internal/domain/entities"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ScopedSigningKeyToProto converts domain ScopedSigningKey to protobuf ScopedSigningKey
func ScopedSigningKeyToProto(key *entities.ScopedSigningKey) *pb.ScopedSigningKey {
	if key == nil {
		return nil
	}

	var respPerm *pb.ResponsePermission
	if key.ResponseMaxMsgs > 0 || key.ResponseTTL > 0 {
		respPerm = &pb.ResponsePermission{
			MaxMsgs: int32(key.ResponseMaxMsgs),
			Expires: int64(key.ResponseTTL),
		}
	}

	return &pb.ScopedSigningKey{
		Id:          UUIDToString(key.ID),
		AccountId:   UUIDToString(key.AccountID),
		Name:        key.Name,
		Description: key.Description,
		PublicKey:   key.PublicKey,
		Permissions: &pb.UserPermissions{
			PubAllow: key.PubAllow,
			PubDeny:  key.PubDeny,
			SubAllow: key.SubAllow,
			SubDeny:  key.SubDeny,
		},
		ResponsePermission: respPerm,
		CreatedAt:          timestamppb.New(key.CreatedAt),
		UpdatedAt:          timestamppb.New(key.UpdatedAt),
	}
}

// ScopedSigningKeysToProto converts slice of domain ScopedSigningKeys to protobuf ScopedSigningKeys
func ScopedSigningKeysToProto(keys []*entities.ScopedSigningKey) []*pb.ScopedSigningKey {
	result := make([]*pb.ScopedSigningKey, len(keys))
	for i, key := range keys {
		result[i] = ScopedSigningKeyToProto(key)
	}
	return result
}

// ProtoToUserPermissions converts protobuf UserPermissions to domain fields
func ProtoToUserPermissions(perms *pb.UserPermissions) ([]string, []string, []string, []string) {
	if perms == nil {
		return nil, nil, nil, nil
	}
	return perms.PubAllow, perms.PubDeny, perms.SubAllow, perms.SubDeny
}

// ProtoToResponsePermission converts protobuf ResponsePermission to domain fields
func ProtoToResponsePermission(resp *pb.ResponsePermission) (int, int64) {
	if resp == nil {
		return 0, 0
	}
	return int(resp.MaxMsgs), resp.Expires
}
