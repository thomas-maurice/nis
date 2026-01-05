package entities

import (
	"time"

	"github.com/google/uuid"
)

// ScopedSigningKey represents a permission template for signing users within an account
type ScopedSigningKey struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Name            string
	Description     string
	EncryptedSeed   string   // Storage reference format
	PublicKey       string   // NATS public key, starts with 'A' (account signing key)
	PubAllow        []string // Publish permissions (subject patterns)
	PubDeny         []string // Publish denials (subject patterns)
	SubAllow        []string // Subscribe permissions (subject patterns)
	SubDeny         []string // Subscribe denials (subject patterns)
	ResponseMaxMsgs int      // Max response messages for request-reply
	ResponseTTL     time.Duration // Time-to-live for responses
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
