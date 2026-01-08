package entities

import (
	"time"

	"github.com/google/uuid"
)

// Cluster represents a NATS server cluster configuration
type Cluster struct {
	ID                   uuid.UUID
	Name                 string
	Description          string
	ServerURLs           []string // NATS server URLs (e.g., ["nats://localhost:4222"])
	OperatorID           uuid.UUID
	SystemAccountPubKey  string // Public key of the system account
	EncryptedCreds       string // Encrypted system account credentials for pushing JWTs
	SkipVerifyTLS        bool    // Skip TLS certificate verification
	Healthy              bool    // Health status of the cluster
	LastHealthCheck      *time.Time // Last time health check was performed
	HealthCheckError     string  // Last health check error message (if any)
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
