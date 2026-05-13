package services

import (
	"context"
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

// JWTService handles generation of NATS JWTs for operators, accounts, and users
type JWTService struct {
	encryptor encryption.Encryptor
}

// NewJWTService creates a new JWT service
func NewJWTService(encryptor encryption.Encryptor) *JWTService {
	return &JWTService{
		encryptor: encryptor,
	}
}

// GenerateOperatorJWT generates a self-signed operator JWT
func (s *JWTService) GenerateOperatorJWT(ctx context.Context, operator *entities.Operator) (string, error) {
	// Decrypt the operator's seed
	seedBytes, err := s.encryptor.Decrypt(ctx, operator.EncryptedSeed)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt operator seed: %w", err)
	}

	// Parse the seed to get the key pair
	kp, err := nkeys.FromSeed(seedBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse operator seed: %w", err)
	}

	// Create operator claims
	claims := jwt.NewOperatorClaims(operator.PublicKey)
	claims.Name = operator.Name

	// Set system account if configured
	if operator.SystemAccountPubKey != "" {
		claims.SystemAccount = operator.SystemAccountPubKey
	}

	// Encode and sign the JWT
	token, err := claims.Encode(kp)
	if err != nil {
		return "", fmt.Errorf("failed to encode operator JWT: %w", err)
	}

	return token, nil
}

// GenerateAccountJWT generates an account JWT signed by the operator.
//
// Each scoped signing key in scopedKeys is declared as a NATS scoped signer in the
// account's `signing_keys` claim, with its pub/sub allow/deny lists and response
// permission carried as the scope template. Without this, NATS rejects every user
// JWT signed by a scoped key as "Authorization Violation" because the signing key
// is not recognised by the account. Pass nil/empty scopedKeys for the simple case
// where only the account's own key signs users.
func (s *JWTService) GenerateAccountJWT(ctx context.Context, account *entities.Account, operator *entities.Operator, scopedKeys []*entities.ScopedSigningKey) (string, error) {
	// Decrypt the operator's seed (operator signs the account JWT)
	operatorSeedBytes, err := s.encryptor.Decrypt(ctx, operator.EncryptedSeed)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt operator seed: %w", err)
	}

	operatorKP, err := nkeys.FromSeed(operatorSeedBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse operator seed: %w", err)
	}

	// Create account claims
	claims := jwt.NewAccountClaims(account.PublicKey)
	claims.Name = account.Name

	// Configure JetStream limits if enabled
	if account.JetStreamEnabled {
		claims.Limits.JetStreamLimits = jwt.JetStreamLimits{
			MemoryStorage:        account.JetStreamMaxMemory,
			DiskStorage:          account.JetStreamMaxStorage,
			Streams:              account.JetStreamMaxStreams,
			Consumer:             account.JetStreamMaxConsumers,
			MemoryMaxStreamBytes: -1,
			DiskMaxStreamBytes:   -1,
		}
	}

	// Register each scoped signing key as a NATS scoped signer. `AddScopedSigner`
	// embeds the template (pub/sub permissions + response limits) into the account
	// JWT so NATS can apply them to any user JWT signed by that key.
	for _, sk := range scopedKeys {
		if sk == nil {
			continue
		}
		scope := jwt.NewUserScope()
		scope.Key = sk.PublicKey
		scope.Role = sk.Name
		scope.Description = sk.Description
		scope.Template.Pub.Allow = sk.PubAllow
		scope.Template.Pub.Deny = sk.PubDeny
		scope.Template.Sub.Allow = sk.SubAllow
		scope.Template.Sub.Deny = sk.SubDeny
		if sk.ResponseMaxMsgs > 0 || sk.ResponseTTL > 0 {
			scope.Template.Resp = &jwt.ResponsePermission{
				MaxMsgs: sk.ResponseMaxMsgs,
				Expires: sk.ResponseTTL,
			}
		}
		claims.SigningKeys.AddScopedSigner(scope)
	}

	// Encode and sign the JWT with operator key
	token, err := claims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode account JWT: %w", err)
	}

	return token, nil
}

// GenerateUserJWT generates a user JWT signed by the account or scoped signing key
func (s *JWTService) GenerateUserJWT(ctx context.Context, user *entities.User, account *entities.Account, scopedKey *entities.ScopedSigningKey) (string, error) {
	// Create user claims
	claims := jwt.NewUserClaims(user.PublicKey)
	claims.Name = user.Name

	var signingKP nkeys.KeyPair
	var err error

	if scopedKey != nil {
		// Sign with scoped signing key
		scopedSeedBytes, err := s.encryptor.Decrypt(ctx, scopedKey.EncryptedSeed)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt scoped key seed: %w", err)
		}

		signingKP, err = nkeys.FromSeed(scopedSeedBytes)
		if err != nil {
			return "", fmt.Errorf("failed to parse scoped key seed: %w", err)
		}

		// Set issuer account
		claims.IssuerAccount = account.PublicKey

		// NATS requires scoped users to have completely empty UserPermissionLimits
		// (`UserScope.ValidateScopedSigner` -> `HasEmptyPermissions`, which does a
		// reflect.DeepEqual against the zero value). `NewUserClaims` pre-fills
		// NatsLimits with NoLimit sentinels, so we have to clear them explicitly.
		// `SetScoped(true)` zeroes the embedded UserPermissionLimits in one shot.
		claims.SetScoped(true)
	} else {
		// Sign with account key directly
		accountSeedBytes, err := s.encryptor.Decrypt(ctx, account.EncryptedSeed)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt account seed: %w", err)
		}

		signingKP, err = nkeys.FromSeed(accountSeedBytes)
		if err != nil {
			return "", fmt.Errorf("failed to parse account seed: %w", err)
		}
	}

	// Encode and sign the JWT
	token, err := claims.Encode(signingKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode user JWT: %w", err)
	}

	return token, nil
}

// GetUserCredentials returns the complete .creds file content for a user
func (s *JWTService) GetUserCredentials(ctx context.Context, user *entities.User) (string, error) {
	// Decrypt the user's seed
	seedBytes, err := s.encryptor.Decrypt(ctx, user.EncryptedSeed)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt user seed: %w", err)
	}

	// Generate the .creds file using the entity helper method
	creds := user.GenerateCredsFile(string(seedBytes))

	return creds, nil
}

// GenerateDeleteClaimJWT generates an operator-signed generic claim JWT for deleting accounts
// This JWT is used with the $SYS.REQ.CLAIMS.DELETE subject to remove accounts from the resolver
func (s *JWTService) GenerateDeleteClaimJWT(ctx context.Context, operator *entities.Operator, accountPublicKeys []string) (string, error) {
	// Decrypt the operator's seed
	seedBytes, err := s.encryptor.Decrypt(ctx, operator.EncryptedSeed)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt operator seed: %w", err)
	}

	// Parse the seed to get the key pair
	kp, err := nkeys.FromSeed(seedBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse operator seed: %w", err)
	}

	// Create generic claims with accounts field for deletion
	claims := jwt.NewGenericClaims(operator.PublicKey)
	claims.Data["accounts"] = accountPublicKeys

	// Encode and sign the JWT
	token, err := claims.Encode(kp)
	if err != nil {
		return "", fmt.Errorf("failed to encode delete claim JWT: %w", err)
	}

	return token, nil
}

// GenerateNKey generates a new NKey pair for the specified prefix type
func GenerateNKey(prefix nkeys.PrefixByte) (seed []byte, publicKey string, err error) {
	kp, err := nkeys.CreatePair(prefix)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create key pair: %w", err)
	}

	seed, err = kp.Seed()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get seed: %w", err)
	}

	publicKey, err = kp.PublicKey()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get public key: %w", err)
	}

	return seed, publicKey, nil
}

// ValidateNKeySeed validates that a seed is valid and returns the public key
func ValidateNKeySeed(seed []byte) (publicKey string, err error) {
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return "", fmt.Errorf("invalid seed: %w", err)
	}

	publicKey, err = kp.PublicKey()
	if err != nil {
		return "", fmt.Errorf("failed to get public key: %w", err)
	}

	return publicKey, nil
}
