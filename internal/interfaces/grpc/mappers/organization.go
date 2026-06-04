package mappers

import (
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OrganizationToProto converts a domain Organization to its protobuf representation.
func OrganizationToProto(org *entities.Organization) *pb.Organization {
	if org == nil {
		return nil
	}
	return &pb.Organization{
		Id:          UUIDToString(org.ID),
		Name:        org.Name,
		Slug:        org.Slug,
		Description: org.Description,
		CreatedAt:   timestamppb.New(org.CreatedAt),
		UpdatedAt:   timestamppb.New(org.UpdatedAt),
	}
}

// SSOConfigToProto converts a domain OrganizationSSOConfig to its protobuf representation.
// The encrypted client secret is NEVER emitted; client_secret_set is true when one is stored.
func SSOConfigToProto(cfg *entities.OrganizationSSOConfig) *pb.OrganizationSSOConfig {
	if cfg == nil {
		return nil
	}
	p := &pb.OrganizationSSOConfig{
		Id:              UUIDToString(cfg.ID),
		OrganizationId:  UUIDToString(cfg.OrganizationID),
		Enabled:         cfg.Enabled,
		IssuerUrl:       cfg.IssuerURL,
		ClientId:        cfg.ClientID,
		ClientSecretSet: cfg.EncryptedClientSecret != "",
		Scopes:          cfg.Scopes,
		GroupClaim:      cfg.GroupClaim,
		CreatedAt:       timestamppb.New(cfg.CreatedAt),
		UpdatedAt:       timestamppb.New(cfg.UpdatedAt),
	}
	if cfg.DefaultRole != nil {
		p.DefaultRole = string(*cfg.DefaultRole)
	}
	return p
}

// SSORoleMappingToProto converts a domain SSORoleMapping to its protobuf representation.
func SSORoleMappingToProto(m *entities.SSORoleMapping) *pb.SSORoleMapping {
	if m == nil {
		return nil
	}
	p := &pb.SSORoleMapping{
		Id:             UUIDToString(m.ID),
		OrganizationId: UUIDToString(m.OrganizationID),
		GroupValue:     m.GroupValue,
		Role:           string(m.Role),
		Priority:       int32(m.Priority),
		CreatedAt:      timestamppb.New(m.CreatedAt),
	}
	if m.ScopeOperatorID != nil {
		p.ScopeOperatorId = UUIDToString(*m.ScopeOperatorID)
	}
	if m.ScopeAccountID != nil {
		p.ScopeAccountId = UUIDToString(*m.ScopeAccountID)
	}
	return p
}
