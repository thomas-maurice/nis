package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// APITokenService manages service-account API tokens. Plaintext is generated
// server-side, hashed before storage, and returned exactly once to the caller.
//
// The middleware ValidateToken path calls Authenticate on every token-authed
// request — that's the hot path. The other methods are administrative.
type APITokenService struct {
	factory persistence.RepositoryFactory
}

func NewAPITokenService(factory persistence.RepositoryFactory) *APITokenService {
	return &APITokenService{factory: factory}
}

// Errors surfaced specifically by token authentication so the middleware can
// distinguish them from generic "not found" or "internal" failures and bucket
// the metrics accordingly. The wrapping uses %w so errors.Is keeps working.
var (
	ErrAPITokenInvalid = errors.New("api token invalid")
	ErrAPITokenExpired = errors.New("api token expired")
	ErrAPITokenRevoked = errors.New("api token revoked")
)

// CreateAPITokenRequest carries the parameters needed to mint a new token. Role
// and scope are decided at creation time and frozen on the token — they do NOT
// re-resolve against the creator's current permissions on later use.
type CreateAPITokenRequest struct {
	Name        string
	Description string
	Role        entities.APIUserRole
	OperatorID  *uuid.UUID
	AccountID   *uuid.UUID
	ExpiresAt   *time.Time // nil = never expires
	// CreatedByUserID identifies the api_user who minted the token. May be nil
	// when an admin uses the offline `nis user create`-style flow (no such flow
	// today, but the FK is nullable to keep that option open).
	CreatedByUserID *uuid.UUID
}

// CreateToken generates a random plaintext token, persists its hash, emits an
// api_token.created event, and returns the entity + plaintext (the latter visible
// once and only once).
func (s *APITokenService) CreateToken(ctx context.Context, req CreateAPITokenRequest) (*entities.APIToken, string, error) {
	if req.Name == "" {
		return nil, "", fmt.Errorf("name is required")
	}
	if !req.Role.IsValid() {
		return nil, "", fmt.Errorf("invalid role: %s", req.Role)
	}
	switch req.Role {
	case entities.RoleOperatorAdmin:
		if req.OperatorID == nil {
			return nil, "", fmt.Errorf("operator_id is required for operator-admin role")
		}
		if req.AccountID != nil {
			return nil, "", fmt.Errorf("account_id must not be set for operator-admin role")
		}
	case entities.RoleAccountAdmin:
		if req.AccountID == nil {
			return nil, "", fmt.Errorf("account_id is required for account-admin role")
		}
		if req.OperatorID != nil {
			return nil, "", fmt.Errorf("operator_id must not be set for account-admin role")
		}
	case entities.RoleAdmin:
		if req.OperatorID != nil || req.AccountID != nil {
			return nil, "", fmt.Errorf("operator_id and account_id must not be set for admin role")
		}
	}

	// 32 bytes of randomness, hex-encoded (64 chars). High enough entropy that
	// SHA-256 is sufficient — bcrypt's salt would add no security here and would
	// cost ~50ms on every authenticated request.
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, "", fmt.Errorf("generate token: %w", err)
	}
	rawHex := hex.EncodeToString(rawBytes)
	plaintext := entities.APITokenPrefix + rawHex

	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])

	// Display prefix is the literal "nis_pat_" plus the first 8 chars of the
	// random portion. Stored at create time so list views never have to touch
	// the secret material to render.
	displayPrefix := entities.APITokenPrefix + rawHex[:8]

	now := time.Now().UTC()
	token := &entities.APIToken{
		ID:              uuid.New(),
		Name:            req.Name,
		TokenHash:       hash,
		Prefix:          displayPrefix,
		Description:     req.Description,
		CreatedByUserID: req.CreatedByUserID,
		Role:            req.Role,
		OperatorID:      req.OperatorID,
		AccountID:       req.AccountID,
		ExpiresAt:       req.ExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.APITokenRepository().Create(ctx, token); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAPITokenCreated,
			OperatorID:   token.OperatorID,
			AccountID:    token.AccountID,
			ResourceType: "api_token",
			ResourceID:   token.ID.String(),
			Payload: map[string]any{
				"name":   token.Name,
				"role":   string(token.Role),
				"prefix": token.Prefix,
			},
		})
	})
	if err != nil {
		return nil, "", err
	}
	return token, plaintext, nil
}

// GetToken returns a token by ID. Does not perform authorization — caller checks.
func (s *APITokenService) GetToken(ctx context.Context, id uuid.UUID) (*entities.APIToken, error) {
	return s.factory.APITokenRepository().GetByID(ctx, id)
}

// ListTokens returns tokens matching the filter. Caller is responsible for
// supplying a filter that respects the caller's permission scope.
func (s *APITokenService) ListTokens(ctx context.Context, filter repositories.APITokenFilter) ([]*entities.APIToken, error) {
	return s.factory.APITokenRepository().List(ctx, filter)
}

// DeleteToken permanently removes the row. Distinct from RevokeToken which
// leaves the row in place with revoked_at set. Delete is used by admins for
// cleanup; revoke is the standard "stop accepting this" flow.
func (s *APITokenService) DeleteToken(ctx context.Context, id uuid.UUID) error {
	return s.factory.APITokenRepository().Delete(ctx, id)
}

// RevokeToken marks the token as revoked. Subsequent Authenticate calls will fail.
// Emits an api_token.revoked event.
func (s *APITokenService) RevokeToken(ctx context.Context, id uuid.UUID) error {
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		token, err := tx.APITokenRepository().GetByID(ctx, id)
		if err != nil {
			return err
		}
		if token.IsRevoked() {
			// Idempotent: do not double-emit the event.
			return nil
		}
		now := time.Now().UTC()
		if err := tx.APITokenRepository().Revoke(ctx, id, now); err != nil {
			return err
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAPITokenRevoked,
			OperatorID:   token.OperatorID,
			AccountID:    token.AccountID,
			ResourceType: "api_token",
			ResourceID:   token.ID.String(),
			Payload: map[string]any{
				"name":   token.Name,
				"prefix": token.Prefix,
			},
		})
	})
}

// Authenticate is the hot path called from the auth middleware. Returns the
// token entity if the plaintext is recognized AND the token is neither expired
// nor revoked. Returns one of ErrAPITokenInvalid / ErrAPITokenExpired /
// ErrAPITokenRevoked so the middleware can bucket metrics by failure reason.
//
// Does NOT update last_used_at — that's done by the coalescing flusher in the
// middleware to avoid hammering the DB on every request.
func (s *APITokenService) Authenticate(ctx context.Context, plaintext string) (*entities.APIToken, error) {
	if plaintext == "" {
		return nil, ErrAPITokenInvalid
	}
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	token, err := s.factory.APITokenRepository().GetByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, ErrAPITokenInvalid
		}
		return nil, fmt.Errorf("lookup api token: %w", err)
	}
	if token.IsRevoked() {
		return nil, ErrAPITokenRevoked
	}
	if token.IsExpired(time.Now().UTC()) {
		return nil, ErrAPITokenExpired
	}
	return token, nil
}

// SyntheticAPIUser builds an APIUser that matches the token's role+scope for
// downstream PermissionService checks. The synthetic user's ID is the token ID
// — it never collides with a real api_users row, and is only used in-context.
func (s *APITokenService) SyntheticAPIUser(token *entities.APIToken) *entities.APIUser {
	return &entities.APIUser{
		ID:         token.ID,
		Username:   "token:" + token.Prefix,
		Role:       token.Role,
		OperatorID: token.OperatorID,
		AccountID:  token.AccountID,
		CreatedAt:  token.CreatedAt,
		UpdatedAt:  token.UpdatedAt,
	}
}
