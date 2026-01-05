package entities

import (
	"time"

	"github.com/google/uuid"
)

// APIUserRole represents the role of an API user
type APIUserRole string

const (
	// RoleAdmin has full access to all operations
	RoleAdmin APIUserRole = "admin"

	// RoleOperatorAdmin can read operators and manage accounts/users/keys
	RoleOperatorAdmin APIUserRole = "operator-admin"

	// RoleAccountAdmin can read accounts and manage users
	RoleAccountAdmin APIUserRole = "account-admin"
)

// APIUser represents a user of the NIS API
type APIUser struct {
	ID           uuid.UUID
	Username     string
	PasswordHash string // bcrypt hash
	Role         APIUserRole
	OperatorID   *uuid.UUID // Required for operator-admin role
	AccountID    *uuid.UUID // Required for account-admin role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsValid checks if the role is a valid API user role
func (r APIUserRole) IsValid() bool {
	switch r {
	case RoleAdmin, RoleOperatorAdmin, RoleAccountAdmin:
		return true
	default:
		return false
	}
}
