package repositories

import "errors"

var (
	// ErrNotFound is returned when an entity is not found in the repository
	ErrNotFound = errors.New("entity not found")

	// ErrAlreadyExists is returned when attempting to create an entity that already exists
	ErrAlreadyExists = errors.New("entity already exists")

	// ErrInvalidCursor is returned when a pagination cursor cannot be decoded.
	ErrInvalidCursor = errors.New("invalid pagination cursor")
)

// ListOptions contains common options for list operations
type ListOptions struct {
	Limit  int
	Offset int
}
