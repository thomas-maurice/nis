// Package oidc wraps the go-oidc/v3 library with a cached provider and
// an injectable Verifier interface. The interface keeps unit tests free of
// live IdP network calls.
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Claims holds the extracted information from a validated OIDC ID token.
type Claims struct {
	Subject           string
	Email             string
	PreferredUsername string
	Groups            []string
}

// Verifier validates an OIDC ID token and extracts claims.
// Implementations:
//   - [RealVerifier] — wraps go-oidc IDTokenVerifier; used in production.
//   - Fake implementations in tests (no live IdP required).
type Verifier interface {
	// Verify validates the ID token's signature, iss, aud, exp, and nonce,
	// then extracts and returns the claims. groupClaim names the token claim
	// that holds the group list (e.g. "groups").
	Verify(ctx context.Context, rawIDToken, expectedNonce, groupClaim string) (*Claims, error)
}

// ProviderCache maintains one discovered go-oidc Provider per issuer URL.
// Discovery is a network round-trip; caching avoids repeating it on every
// login initiation.
type ProviderCache struct {
	mu        sync.Mutex
	providers map[string]*gooidc.Provider
}

// NewProviderCache creates an empty provider cache.
func NewProviderCache() *ProviderCache {
	return &ProviderCache{
		providers: make(map[string]*gooidc.Provider),
	}
}

// GetOrDiscover returns a cached provider for issuerURL, or runs OIDC discovery
// and caches the result. Concurrent callers for the same issuer will each run
// discovery; the last one wins (safe, identical results expected for the same
// issuer URL within a server lifecycle).
func (c *ProviderCache) GetOrDiscover(ctx context.Context, issuerURL string) (*gooidc.Provider, error) {
	c.mu.Lock()
	p, ok := c.providers[issuerURL]
	c.mu.Unlock()
	if ok {
		return p, nil
	}

	p, err := gooidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuerURL, err)
	}

	c.mu.Lock()
	c.providers[issuerURL] = p
	c.mu.Unlock()
	return p, nil
}

// BuildOAuthConfig constructs an *oauth2.Config for the given provider and
// SSO config values.
func BuildOAuthConfig(provider *gooidc.Provider, clientID, clientSecret, redirectURL string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}
}

// RealVerifier implements Verifier using go-oidc's IDTokenVerifier.
type RealVerifier struct {
	verifier *gooidc.IDTokenVerifier
}

// NewRealVerifier creates a RealVerifier bound to provider + clientID.
func NewRealVerifier(provider *gooidc.Provider, clientID string) *RealVerifier {
	return &RealVerifier{
		verifier: provider.Verifier(&gooidc.Config{ClientID: clientID}),
	}
}

// Verify validates the raw ID token, checks the nonce, and extracts claims.
func (v *RealVerifier) Verify(ctx context.Context, rawIDToken, expectedNonce, groupClaim string) (*Claims, error) {
	idToken, err := v.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("id token verification failed: %w", err)
	}

	// Verify nonce (replay protection).
	var rawClaims map[string]any
	if err := idToken.Claims(&rawClaims); err != nil {
		return nil, fmt.Errorf("extract raw claims: %w", err)
	}

	nonce, _ := rawClaims["nonce"].(string)
	if nonce != expectedNonce {
		return nil, fmt.Errorf("nonce mismatch: expected %q, got %q", expectedNonce, nonce)
	}

	// Extract standard claims.
	email, _ := rawClaims["email"].(string)
	preferredUsername, _ := rawClaims["preferred_username"].(string)

	// Extract groups from the configured claim.
	var groups []string
	if groupClaim != "" {
		groups = extractStringSlice(rawClaims, groupClaim)
	}

	return &Claims{
		Subject:           idToken.Subject,
		Email:             email,
		PreferredUsername: preferredUsername,
		Groups:            groups,
	}, nil
}

// extractStringSlice pulls a string slice from a raw claims map under key.
// Returns nil if the key is absent or not a []any of strings.
func extractStringSlice(raw map[string]any, key string) []string {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// GenerateState returns a high-entropy opaque string for the OIDC state parameter.
func GenerateState() (string, error) {
	return randomBase64URL(32)
}

// GenerateNonce returns a high-entropy opaque string for the OIDC nonce parameter.
func GenerateNonce() (string, error) {
	return randomBase64URL(32)
}

// randomBase64URL returns n random bytes encoded as unpadded base64url.
func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
