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

	// JWT lifecycle policy (P2). All zero values mean "no expiry / never renew" —
	// the back-compat default. Set per operator via SetJWTPolicy.
	//
	// UserJWTTTL    : default TTL applied to a user JWT at mint time when the user
	//                 has no per-row override. 0 = no expiry.
	// AccountJWTTTL : TTL applied to the account JWT at sign time. 0 = no expiry.
	// JWTWarnWindow : how far ahead of `exp` the sweeper fires user.cred.expiring_soon.
	//                 Default 14 days. Ignored when UserJWTTTL == 0.
	// JWTAutoRenew  : when true, the sweeper re-signs user JWTs that fall inside the
	//                 warn window. Does NOT renew already-expired JWTs — those emit
	//                 user.cred.expired and require explicit RegenerateUserJWT.
	UserJWTTTL    time.Duration
	AccountJWTTTL time.Duration
	JWTWarnWindow time.Duration
	JWTAutoRenew  bool

	// Backups (P12). Default disabled. BackupInterval is the gap between
	// scheduled backups; nil means "no interval set". BackupRetention nil
	// means "keep forever"; explicit N means "keep last N". LastBackupAt
	// is updated by BackupService.RunBackup.
	BackupEnabled   bool
	BackupInterval  *time.Duration
	BackupRetention *int
	LastBackupAt    *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}
