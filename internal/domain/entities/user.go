package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// User represents an individual NATS connection credential
type User struct {
	ID                  uuid.UUID
	AccountID           uuid.UUID
	Name                string
	Description         string
	EncryptedSeed       string     // Storage reference format
	PublicKey           string     // NATS public key, starts with 'U'
	JWT                 string     // User JWT (signed by account or scoped key)
	ScopedSigningKeyID  *uuid.UUID // Optional: if signed by a scoped signing key
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// GenerateCredsFile returns the full .creds file content for this user
// The seed parameter must be the decrypted NKey seed
func (u *User) GenerateCredsFile(seed string) string {
	return fmt.Sprintf(`-----BEGIN NATS USER JWT-----
%s
------END NATS USER JWT------

************************* IMPORTANT *************************
NKEY Seed printed below can be used to sign and prove identity.
NKEYs are sensitive and should be treated as secrets.

-----BEGIN USER NKEY SEED-----
%s
------END USER NKEY SEED------

*************************************************************
`, u.JWT, seed)
}
