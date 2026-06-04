package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	oidcinfra "github.com/thomas-maurice/nis/internal/infrastructure/oidc"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ErrSSOUnavailable is returned by StartLogin when the org is missing, SSO is
// not configured, or SSO is disabled. The HTTP handler maps this to a generic
// 400 to avoid leaking which step failed.
var ErrSSOUnavailable = errors.New("sso unavailable")

// ErrSSODenied is returned by CompleteLogin when group mapping fails, the state
// is invalid, or any verification step rejects the token.
var ErrSSODenied = errors.New("sso login denied")

// defaultOIDCScopes is applied when the SSO config specifies no scopes.
const defaultOIDCScopes = "openid profile email groups"

// VerifierFactory constructs an oidcinfra.Verifier from the discovered provider
// and client ID. Production callers pass RealVerifierFactory(); tests inject a
// fake.
type VerifierFactory func(provider *gooidc.Provider, clientID string) oidcinfra.Verifier

// RealVerifierFactory is the production VerifierFactory; it wraps go-oidc's
// IDTokenVerifier.
func RealVerifierFactory(provider *gooidc.Provider, clientID string) oidcinfra.Verifier {
	return oidcinfra.NewRealVerifier(provider, clientID)
}

// SSOService orchestrates the OIDC authorization-code + PKCE login flow.
type SSOService struct {
	factory       persistence.RepositoryFactory
	encryptor     encryption.Encryptor
	authService   *AuthService
	providerCache *oidcinfra.ProviderCache
	publicURL     string
	stateTTL      time.Duration
}

// NewSSOService creates a new SSOService.
func NewSSOService(
	factory persistence.RepositoryFactory,
	encryptor encryption.Encryptor,
	authService *AuthService,
	providerCache *oidcinfra.ProviderCache,
	publicURL string,
	stateTTL time.Duration,
) *SSOService {
	if stateTTL <= 0 {
		stateTTL = 10 * time.Minute
	}
	return &SSOService{
		factory:       factory,
		encryptor:     encryptor,
		authService:   authService,
		providerCache: providerCache,
		publicURL:     publicURL,
		stateTTL:      stateTTL,
	}
}

// StartLogin initiates the OIDC authorization-code + PKCE flow for the org
// identified by slug. Returns the authorization URL to redirect the browser to.
//
// Any error (unknown slug, SSO disabled, discovery failure) returns
// ErrSSOUnavailable so callers cannot distinguish individual failure modes.
func (s *SSOService) StartLogin(ctx context.Context, slug string, redirectAfter *string) (string, error) {
	org, err := s.factory.OrganizationRepository().GetBySlug(ctx, slug)
	if err != nil {
		return "", ErrSSOUnavailable
	}

	cfg, err := s.factory.OrganizationSSOConfigRepository().GetByOrganizationID(ctx, org.ID)
	if err != nil || !cfg.Enabled {
		return "", ErrSSOUnavailable
	}

	// Decrypt client secret in-memory. Never logs or returns the secret.
	secretBytes, err := s.encryptor.Decrypt(ctx, cfg.EncryptedClientSecret)
	if err != nil {
		return "", ErrSSOUnavailable
	}
	clientSecret := string(secretBytes)

	// OIDC discovery (cached). External network call — must NOT be inside a tx.
	provider, err := s.providerCache.GetOrDiscover(ctx, cfg.IssuerURL)
	if err != nil {
		return "", ErrSSOUnavailable
	}

	oauthCfg := oidcinfra.BuildOAuthConfig(
		provider,
		cfg.ClientID,
		clientSecret,
		s.callbackURL(),
		scopeList(cfg.Scopes),
	)

	state, err := oidcinfra.GenerateState()
	if err != nil {
		return "", ErrSSOUnavailable
	}
	nonce, err := oidcinfra.GenerateNonce()
	if err != nil {
		return "", ErrSSOUnavailable
	}
	pkceVerifier := oauth2.GenerateVerifier()

	now := clock.Now()
	loginState := &entities.OIDCLoginState{
		State:          state,
		OrganizationID: org.ID,
		Nonce:          nonce,
		PKCEVerifier:   pkceVerifier,
		RedirectAfter:  redirectAfter,
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.stateTTL),
	}
	if err := s.factory.OIDCLoginStateRepository().Create(ctx, loginState); err != nil {
		return "", ErrSSOUnavailable
	}

	authURL := oauthCfg.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.S256ChallengeOption(pkceVerifier),
	)
	return authURL, nil
}

// CompleteLogin handles the OIDC callback. It validates state, exchanges the
// code for tokens, verifies the ID token, maps groups to a role, JIT provisions
// the api_user, and issues a NIS session JWT.
//
// verifierFn allows tests to inject a fake Verifier. Production callers pass
// RealVerifierFactory.
func (s *SSOService) CompleteLogin(ctx context.Context, code, state string, verifierFn VerifierFactory) (sessionJWT string, redirectAfter *string, err error) {
	// Atomically consume the state row.
	st, err := s.factory.OIDCLoginStateRepository().GetAndDelete(ctx, state)
	if err != nil {
		return "", nil, fmt.Errorf("%w: invalid or expired state", ErrSSODenied)
	}
	if clock.Now().After(st.ExpiresAt) {
		return "", nil, fmt.Errorf("%w: state expired", ErrSSODenied)
	}

	org, err := s.factory.OrganizationRepository().GetByID(ctx, st.OrganizationID)
	if err != nil {
		return "", nil, fmt.Errorf("%w: organization not found", ErrSSODenied)
	}
	cfg, err := s.factory.OrganizationSSOConfigRepository().GetByOrganizationID(ctx, org.ID)
	if err != nil || !cfg.Enabled {
		return "", nil, fmt.Errorf("%w: sso not configured or disabled", ErrSSODenied)
	}

	secretBytes, err := s.encryptor.Decrypt(ctx, cfg.EncryptedClientSecret)
	if err != nil {
		return "", nil, fmt.Errorf("%w: decrypt client secret", ErrSSODenied)
	}
	clientSecret := string(secretBytes)

	// External network calls must NOT run inside a tx.
	provider, err := s.providerCache.GetOrDiscover(ctx, cfg.IssuerURL)
	if err != nil {
		return "", nil, fmt.Errorf("%w: oidc discovery failed", ErrSSODenied)
	}

	oauthCfg := oidcinfra.BuildOAuthConfig(
		provider,
		cfg.ClientID,
		clientSecret,
		s.callbackURL(),
		scopeList(cfg.Scopes),
	)

	// Code exchange. External network call.
	token, err := oauthCfg.Exchange(ctx, code, oauth2.VerifierOption(st.PKCEVerifier))
	if err != nil {
		return "", nil, fmt.Errorf("%w: token exchange failed", ErrSSODenied)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", nil, fmt.Errorf("%w: id_token missing from token response", ErrSSODenied)
	}

	groupClaim := cfg.GroupClaim
	if groupClaim == "" {
		groupClaim = "groups"
	}
	verifier := verifierFn(provider, cfg.ClientID)
	claims, err := verifier.Verify(ctx, rawIDToken, st.Nonce, groupClaim)
	if err != nil {
		return "", nil, fmt.Errorf("%w: id token verification failed", ErrSSODenied)
	}

	// Load and sort mappings (repo returns DESC; we need ASC — lower Priority = first).
	mappings, err := s.factory.SSORoleMappingRepository().ListByOrganization(ctx, org.ID, repositories.SSORoleMappingListFilter{})
	if err != nil {
		return "", nil, fmt.Errorf("load role mappings: %w", err)
	}
	role, scopeOpID, scopeAccID, mapErr := mapGroupsToRole(mappings, claims.Groups, cfg.DefaultRole)
	if mapErr != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrSSODenied, mapErr)
	}

	// JIT find-or-create inside a single tx.
	var user *entities.APIUser
	txErr := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		existing, findErr := tx.APIUserRepository().GetByExternalSubject(ctx, org.ID, claims.Subject)
		if findErr != nil && !errors.Is(findErr, repositories.ErrNotFound) {
			return fmt.Errorf("lookup oidc user: %w", findErr)
		}

		now := clock.Now()
		if errors.Is(findErr, repositories.ErrNotFound) {
			username := chooseUsername(claims)
			orgIDCopy := org.ID
			subjectCopy := claims.Subject
			newUser := &entities.APIUser{
				ID:              uuid.New(),
				Username:        username,
				PasswordHash:    "",
				Role:            role,
				OperatorID:      scopeOpID,
				AccountID:       scopeAccID,
				OrganizationID:  &orgIDCopy,
				AuthSource:      "oidc",
				ExternalSubject: &subjectCopy,
				Email:           ptrStr(claims.Email),
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := tx.APIUserRepository().Create(ctx, newUser); err != nil {
				return fmt.Errorf("create oidc user: %w", err)
			}
			if err := events.EmitTx(ctx, tx, events.Event{
				Type:         entities.EventTypeAPIUserSSOProvisioned,
				ResourceType: "api_user",
				ResourceID:   newUser.ID.String(),
				Payload: map[string]any{
					"organization_id": org.ID.String(),
					"subject":         claims.Subject,
					"email":           claims.Email,
					"username":        username,
					"role":            string(role),
					"groups":          claims.Groups,
				},
			}); err != nil {
				return fmt.Errorf("emit api_user.sso_provisioned: %w", err)
			}
			user = newUser
		} else {
			// Sync role/email/username/scope on every login.
			existing.Role = role
			existing.OperatorID = scopeOpID
			existing.AccountID = scopeAccID
			if claims.Email != "" {
				existing.Email = ptrStr(claims.Email)
			}
			existing.Username = chooseUsername(claims)
			existing.UpdatedAt = now
			if err := tx.APIUserRepository().Update(ctx, existing); err != nil {
				return fmt.Errorf("update oidc user: %w", err)
			}
			user = existing
		}
		return nil
	})
	if txErr != nil {
		return "", nil, txErr
	}

	// Emit login event after the tx commits (EmitSystem opens its own tx).
	_ = events.EmitSystem(ctx, s.factory, events.Event{
		Type:         entities.EventTypeAPIUserSSOLogin,
		ResourceType: "api_user",
		ResourceID:   user.ID.String(),
		Payload: map[string]any{
			"organization_id": org.ID.String(),
			"subject":         claims.Subject,
			"email":           claims.Email,
			"username":        user.Username,
			"role":            string(role),
			"groups":          claims.Groups,
		},
	})

	sessionJWT, err = s.authService.IssueSessionForUser(user)
	if err != nil {
		return "", nil, fmt.Errorf("issue session JWT: %w", err)
	}
	return sessionJWT, st.RedirectAfter, nil
}

// mapGroupsToRole maps IdP groups to a NIS role via the org's ordered mappings.
// Pure function — no side effects, suitable for direct unit testing.
//
// Mappings are sorted ASCENDING by Priority (lower = evaluated first). First
// match wins. Falls back to defaultRole when provided. Returns an error on
// no-match or forbidden role (admin).
func mapGroupsToRole(
	mappings []*entities.SSORoleMapping,
	groups []string,
	defaultRole *entities.APIUserRole,
) (role entities.APIUserRole, scopeOpID *uuid.UUID, scopeAccID *uuid.UUID, err error) {
	// Re-sort ascending (repo returns DESC).
	sorted := make([]*entities.SSORoleMapping, len(mappings))
	copy(sorted, mappings)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	groupSet := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupSet[g] = struct{}{}
	}

	for _, m := range sorted {
		if _, ok := groupSet[m.GroupValue]; !ok {
			continue
		}
		// Hard guard: platform admin can never come from SSO.
		if m.Role == entities.RoleAdmin {
			return "", nil, nil, fmt.Errorf("platform admin role cannot be assigned via SSO")
		}
		// Scope consistency guards (defense-in-depth; mapping write already validates).
		if m.Role == entities.RoleOperatorAdmin && m.ScopeOperatorID == nil {
			return "", nil, nil, fmt.Errorf("operator-admin mapping is missing scope_operator_id")
		}
		if m.Role == entities.RoleAccountAdmin && m.ScopeAccountID == nil {
			return "", nil, nil, fmt.Errorf("account-admin mapping is missing scope_account_id")
		}
		return m.Role, m.ScopeOperatorID, m.ScopeAccountID, nil
	}

	if defaultRole != nil {
		if *defaultRole == entities.RoleAdmin {
			return "", nil, nil, fmt.Errorf("platform admin role cannot be assigned via SSO")
		}
		return *defaultRole, nil, nil, nil
	}

	return "", nil, nil, fmt.Errorf("no group mapping matched and no default role is configured")
}

// callbackURL returns the full redirect URI registered with the IdP.
func (s *SSOService) callbackURL() string {
	return s.publicURL + "/auth/oidc/callback"
}

// scopeList parses the space-separated scope config value. Defaults to the
// standard OIDC+groups scopes when empty.
func scopeList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultOIDCScopes
	}
	return strings.Fields(raw)
}

// chooseUsername derives a display username from OIDC claims.
// Order: preferred_username → email → subject.
func chooseUsername(claims *oidcinfra.Claims) string {
	if claims.PreferredUsername != "" {
		return claims.PreferredUsername
	}
	if claims.Email != "" {
		return claims.Email
	}
	return claims.Subject
}

// ptrStr returns a pointer to s, or nil when s is empty.
func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
