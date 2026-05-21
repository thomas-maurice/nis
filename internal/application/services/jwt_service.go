package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

// freshUniquenessTag returns a string suitable for stamping into a JWT's tag
// list so that two encodes of otherwise-identical claims produce different
// tokens.
//
// Background: jwt v2's ClaimsData.encode unconditionally sets IssuedAt to
// `clock.Now().Unix()` and the ID (jti) to a SHA512/256 hash of the
// remaining claim contents. The Ed25519 signature over those bytes is
// deterministic. Two regenerations within the same Unix second of an
// otherwise-identical claim set therefore produce byte-identical JWTs.
// That breaks the "fresh creds" expectation operators have when calling
// RegenerateUserCredentials. Stuffing a unique tag varies the claim content,
// which varies the auto-generated jti, which varies the signature.
//
// NATS treats user-JWT tags as opaque metadata (no permission impact). We
// prefix with `nis:uniq:` so operators inspecting a JWT understand why the
// tag is there.
func freshUniquenessTag() string {
	return "nis:uniq:" + uuid.NewString()
}

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
// scopedKeys are declared as NATS scoped signers in the `signing_keys` claim
// (see jwt_service.go history for the E1 fix that made this load-bearing).
//
// revocations are flattened into the account's NATS Revocations map. Any user
// JWT issued before each entry's revoked_at and signed by the matching user
// public key will be rejected by NATS. Pass nil/empty for no revocations.
// Pruned rows (PrunedAt != nil) are skipped — they exist only as bookkeeping.
//
// ttl, when > 0, sets the account JWT's `exp`. 0 leaves the JWT without exp
// (the back-compat default; lifecycle is opt-in per operator).
func (s *JWTService) GenerateAccountJWT(ctx context.Context, account *entities.Account, operator *entities.Operator, scopedKeys []*entities.ScopedSigningKey, revocations []*entities.UserJWTRevocation, ttl time.Duration) (string, error) {
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
	//
	// IsPlainSigner SSKs are the exception — they get added as plain
	// strings via SigningKeys.Add. Those are the ones we imported from
	// an NSC store where the original account JWT carried the key as a
	// raw string in signing_keys. NATS then treats the user JWT's own
	// perms as authoritative (matches what the operator's pre-NIS
	// tooling expected), and avoids the "SetScoped zeroes user limits +
	// account JWT carries plain signer = user locked out" trap that
	// hits the system user on imported operators.
	for _, sk := range scopedKeys {
		if sk == nil {
			continue
		}
		if sk.IsPlainSigner {
			claims.SigningKeys.Add(sk.PublicKey)
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
		// Emit Resp when the SSK either declares an explicit response
		// limit OR has a restricted pub_allow. The second case is the
		// load-bearing one: NATS's validateResponsePermissions
		// (server/auth.go) flips publish from "allow anything not
		// denied" to "allow only what's in pub_allow + auto-granted
		// reply inboxes" the moment Resp is present. Emitting it
		// unconditionally would silently break every "permissive" SSK
		// (nil/empty pub_allow). Emitting it only when pub_allow is
		// restrictive gives services the NATS-side default auto-grant
		// (1 msg / 2 min from server/const.go:DEFAULT_ALLOW_RESPONSE_*)
		// without forcing them to list _INBOX.> manually, while
		// leaving permissive keys alone.
		if sk.ResponseMaxMsgs > 0 || sk.ResponseTTL > 0 || len(sk.PubAllow) > 0 {
			scope.Template.Resp = &jwt.ResponsePermission{
				MaxMsgs: sk.ResponseMaxMsgs,
				Expires: sk.ResponseTTL,
			}
		}
		claims.SigningKeys.AddScopedSigner(scope)
	}

	// Flatten active revocations into AccountClaims.Revocations. The
	// Revocations RevocationList map stores `public_key -> revoked_at unix
	// seconds`; NATS rejects any user JWT whose iat is before this timestamp.
	// claims.RevokeAt initialises the map if nil and uses the timestamp we
	// pass (claims.Revoke would default to time.Now).
	for _, rv := range revocations {
		if rv == nil || rv.PrunedAt != nil {
			continue
		}
		claims.RevokeAt(rv.UserPublicKey, rv.RevokedAt)
	}

	if ttl > 0 {
		now := clock.Now()
		claims.IssuedAt = now.Unix()
		claims.Expires = now.Add(ttl).Unix()
	}
	// One-shot tag so two encodes with otherwise-identical claims produce
	// distinct tokens — see freshUniquenessTag's docstring.
	claims.Tags = append(claims.Tags, freshUniquenessTag())

	// Encode and sign the JWT with operator key
	token, err := claims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode account JWT: %w", err)
	}

	return token, nil
}

// UserJWTMint is the output of GenerateUserJWT — token plus the iat/exp the
// caller needs to persist on the user row so the sweeper and the
// AccountClaims.Revocations bookkeeping work correctly.
type UserJWTMint struct {
	Token     string
	IssuedAt  time.Time
	ExpiresAt *time.Time // nil when ttl <= 0
}

// GenerateUserJWT generates a user JWT signed by the account or scoped signing
// key. When ttl > 0 the JWT carries iat + exp; when ttl <= 0 the JWT is
// unbounded (the back-compat default).
//
// The returned UserJWTMint.IssuedAt is also the JWT's `iat` and is what the
// sweeper uses to dedup expiring-soon / expired alerts — pin it on the user
// row exactly.
func (s *JWTService) GenerateUserJWT(ctx context.Context, user *entities.User, account *entities.Account, scopedKey *entities.ScopedSigningKey, ttl time.Duration) (*UserJWTMint, error) {
	// Create user claims
	claims := jwt.NewUserClaims(user.PublicKey)
	claims.Name = user.Name

	var signingKP nkeys.KeyPair
	var err error

	if scopedKey != nil {
		// Sign with scoped signing key
		scopedSeedBytes, err := s.encryptor.Decrypt(ctx, scopedKey.EncryptedSeed)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt scoped key seed: %w", err)
		}

		signingKP, err = nkeys.FromSeed(scopedSeedBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse scoped key seed: %w", err)
		}

		// Set issuer account
		claims.IssuerAccount = account.PublicKey

		// NATS requires scoped users to have completely empty UserPermissionLimits
		// (`UserScope.ValidateScopedSigner` -> `HasEmptyPermissions`, which does a
		// reflect.DeepEqual against the zero value). `NewUserClaims` pre-fills
		// NatsLimits with NoLimit sentinels, so we have to clear them explicitly.
		// `SetScoped(true)` zeroes the embedded UserPermissionLimits in one shot.
		//
		// Skip SetScoped when the SSK is an IsPlainSigner: the parent
		// account JWT lists it as a plain signing_keys string (not a
		// UserScope), so NATS does NOT apply any template — it uses the
		// user JWT's own perms. A SetScoped-zeroed user under a plain
		// signer ends up with subs:0/payload:0 and cannot do anything
		// (this was the symptom on NSC-imported operators where NIS
		// signed the auto-created "system" user with the imported
		// plain signing key and the cluster healthcheck silently timed
		// out, with the JWT push hitting "maximum payload exceeded").
		if !scopedKey.IsPlainSigner {
			claims.SetScoped(true)
		}
	} else {
		// Sign with account key directly
		accountSeedBytes, err := s.encryptor.Decrypt(ctx, account.EncryptedSeed)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt account seed: %w", err)
		}

		signingKP, err = nkeys.FromSeed(accountSeedBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse account seed: %w", err)
		}
	}

	// Stamp iat unconditionally (cheap, and revocation comparisons against
	// iat are how NATS decides whether a user JWT is revoked). When ttl > 0
	// also stamp exp; otherwise leave it zero so claims.Encode emits no exp.
	//
	// jwt v2's Encode will OVERWRITE both IssuedAt and ID, so the only way to
	// make sequential calls produce distinct tokens is to vary a non-ignored
	// claim field. We stuff a one-shot tag (see freshUniquenessTag).
	now := clock.Now()
	claims.IssuedAt = now.Unix()
	claims.Tags = append(claims.Tags, freshUniquenessTag())
	var exp *time.Time
	if ttl > 0 {
		expT := now.Add(ttl)
		claims.Expires = expT.Unix()
		exp = &expT
	}

	// Encode and sign the JWT
	token, err := claims.Encode(signingKP)
	if err != nil {
		return nil, fmt.Errorf("failed to encode user JWT: %w", err)
	}

	return &UserJWTMint{Token: token, IssuedAt: now, ExpiresAt: exp}, nil
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
