package entities

import (
	"time"

	"github.com/google/uuid"
)

// Account represents a NATS account, a multi-tenancy boundary
type Account struct {
	ID                     uuid.UUID
	OperatorID             uuid.UUID
	Name                   string
	Description            string
	EncryptedSeed          string // Storage reference format
	PublicKey              string // NATS public key, starts with 'A'
	JWT                    string // Account JWT (signed by operator)
	JetStreamEnabled       bool
	JetStreamMaxMemory     int64 // -1 = unlimited
	JetStreamMaxStorage    int64 // -1 = unlimited
	JetStreamMaxStreams    int64 // -1 = unlimited
	JetStreamMaxConsumers  int64 // -1 = unlimited
	CreatedAt              time.Time
	UpdatedAt              time.Time
}
