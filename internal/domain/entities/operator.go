package entities

import (
	"time"

	"github.com/google/uuid"
)

// Operator represents a NATS operator, the root of trust in JWT authentication
type Operator struct {
	ID                  uuid.UUID
	Name                string
	Description         string
	EncryptedSeed       string // Storage reference format: "encrypted:<key_id>:<base64_ciphertext>"
	PublicKey           string // NATS public key, starts with 'O'
	JWT                 string // Operator JWT (self-signed)
	SystemAccountPubKey string // Optional: public key of the designated system account
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
