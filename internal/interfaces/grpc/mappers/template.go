package mappers

import (
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TemplateToProto converts a domain Template to its proto shape.
func TemplateToProto(t *entities.Template) *pb.Template {
	if t == nil {
		return nil
	}
	return &pb.Template{
		Id:            UUIDToString(t.ID),
		OperatorId:    UUIDToString(t.OperatorID),
		Name:          t.Name,
		Description:   t.Description,
		LatestVersion: int32(t.LatestVersion),
		CreatedAt:     timestamppb.New(t.CreatedAt),
		UpdatedAt:     timestamppb.New(t.UpdatedAt),
	}
}

func TemplatesToProto(ts []*entities.Template) []*pb.Template {
	out := make([]*pb.Template, len(ts))
	for i, t := range ts {
		out[i] = TemplateToProto(t)
	}
	return out
}

// TemplateVersionToProto converts a domain TemplateVersion to its proto shape.
// Mirrors ScopedSigningKey's permissions+response_permission shape so the UI
// can render template versions and SKKs side-by-side without two converters.
func TemplateVersionToProto(v *entities.TemplateVersion) *pb.TemplateVersion {
	if v == nil {
		return nil
	}
	var respPerm *pb.ResponsePermission
	if v.ResponseMaxMsgs > 0 || v.ResponseTTL > 0 {
		respPerm = &pb.ResponsePermission{
			MaxMsgs: int32(v.ResponseMaxMsgs),
			Expires: int64(v.ResponseTTL),
		}
	}
	out := &pb.TemplateVersion{
		Id:            UUIDToString(v.ID),
		TemplateId:    UUIDToString(v.TemplateID),
		VersionNumber: int32(v.VersionNumber),
		Permissions: &pb.UserPermissions{
			PubAllow: v.PubAllow,
			PubDeny:  v.PubDeny,
			SubAllow: v.SubAllow,
			SubDeny:  v.SubDeny,
		},
		ResponsePermission: respPerm,
		ChangeNote:         v.ChangeNote,
		CreatedAt:          timestamppb.New(v.CreatedAt),
	}
	if v.CreatedByUserID != nil {
		out.CreatedByUserId = UUIDToString(*v.CreatedByUserID)
	}
	return out
}

func TemplateVersionsToProto(vs []*entities.TemplateVersion) []*pb.TemplateVersion {
	out := make([]*pb.TemplateVersion, len(vs))
	for i, v := range vs {
		out[i] = TemplateVersionToProto(v)
	}
	return out
}
