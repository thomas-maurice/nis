package mappers

import (
	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	pb "github.com/thomas-maurice/nis/gen/nis/v1"
)

// ProtoToListOptions converts protobuf ListOptions to domain ListOptions
func ProtoToListOptions(opts *pb.ListOptions) repositories.ListOptions {
	if opts == nil {
		return repositories.ListOptions{}
	}
	return repositories.ListOptions{
		Limit:  int(opts.Limit),
		Offset: int(opts.Offset),
	}
}

// ParseUUID parses a string UUID and returns error if invalid
func ParseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

// UUIDToString converts UUID to string
func UUIDToString(id uuid.UUID) string {
	return id.String()
}

// StringPtr returns pointer to string
func StringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
