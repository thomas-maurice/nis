package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// Client wraps NATS connection for JWT resolver operations
type Client struct {
	nc *nats.Conn
}

// ClientConfig contains configuration for NATS client
type ClientConfig struct {
	ServerURLs []string
	CredsFile  string // Path to credentials file
	Timeout    time.Duration
}

// NewClient creates a new NATS client
func NewClient(cfg ClientConfig) (*Client, error) {
	if len(cfg.ServerURLs) == 0 {
		return nil, fmt.Errorf("at least one server URL is required")
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	// Connection options
	opts := []nats.Option{
		nats.Timeout(cfg.Timeout),
		nats.Name("NATS Identity Service"),
		nats.MaxReconnects(-1), // Unlimited reconnects
		nats.ReconnectWait(2 * time.Second),
	}

	// Add credentials if provided
	if cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	}

	// Connect to NATS
	nc, err := nats.Connect(
		cfg.ServerURLs[0], // Primary server
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	return &Client{nc: nc}, nil
}

// NewClientFromCreds creates a NATS client using credentials content directly
func NewClientFromCreds(serverURLs []string, credsContent string) (*Client, error) {
	if len(serverURLs) == 0 {
		return nil, fmt.Errorf("at least one server URL is required")
	}

	opts := []nats.Option{
		nats.Timeout(10 * time.Second),
		nats.Name("NATS Identity Service"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
	}

	// Add credentials from content
	if credsContent != "" {
		// Parse the creds file to extract JWT and seed
		jwt, seed, err := parseCredsContent(credsContent)
		if err != nil {
			return nil, fmt.Errorf("failed to parse credentials: %w", err)
		}

		// Use UserJWTAndSeed to authenticate
		opts = append(opts, nats.UserJWTAndSeed(jwt, seed))
	}

	nc, err := nats.Connect(serverURLs[0], opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	return &Client{nc: nc}, nil
}

// parseCredsContent parses a .creds file content and extracts JWT and seed
func parseCredsContent(credsContent string) (string, string, error) {
	// The creds file format is:
	// -----BEGIN NATS USER JWT-----
	// <jwt>
	// ------END NATS USER JWT------
	//
	// ************************* IMPORTANT *************************
	// NKEY Seed printed below can be used to sign and prove identity.
	// NKEYs are sensitive and should be treated as secrets.
	//
	// -----BEGIN USER NKEY SEED-----
	// <seed>
	// ------END USER NKEY SEED------

	var jwt, seed string

	// Extract JWT
	jwtStart := "-----BEGIN NATS USER JWT-----"
	jwtEnd := "------END NATS USER JWT------"

	jwtStartIdx := len(jwtStart)
	startIdx := 0
	if idx := find(credsContent, jwtStart); idx >= 0 {
		startIdx = idx + jwtStartIdx
	} else {
		return "", "", fmt.Errorf("JWT start marker not found")
	}

	endIdx := len(credsContent)
	if idx := find(credsContent, jwtEnd); idx >= 0 {
		endIdx = idx
	} else {
		return "", "", fmt.Errorf("JWT end marker not found")
	}

	jwt = trim(credsContent[startIdx:endIdx])

	// Extract seed
	seedStart := "-----BEGIN USER NKEY SEED-----"
	seedEnd := "------END USER NKEY SEED------"

	seedStartIdx := len(seedStart)
	startIdx = 0
	if idx := find(credsContent, seedStart); idx >= 0 {
		startIdx = idx + seedStartIdx
	} else {
		return "", "", fmt.Errorf("seed start marker not found")
	}

	endIdx = len(credsContent)
	if idx := find(credsContent, seedEnd); idx >= 0 {
		endIdx = idx
	} else {
		return "", "", fmt.Errorf("seed end marker not found")
	}

	seed = trim(credsContent[startIdx:endIdx])

	if jwt == "" || seed == "" {
		return "", "", fmt.Errorf("failed to extract JWT or seed from credentials")
	}

	return jwt, seed, nil
}

// find returns the index of substr in s, or -1 if not found
func find(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// trim removes leading and trailing whitespace
func trim(s string) string {
	start := 0
	end := len(s)

	// Trim leading whitespace
	for start < len(s) && isWhitespace(s[start]) {
		start++
	}

	// Trim trailing whitespace
	for end > start && isWhitespace(s[end-1]) {
		end--
	}

	return s[start:end]
}

// isWhitespace returns true if c is a whitespace character
func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// Close closes the NATS connection
func (c *Client) Close() error {
	if c.nc != nil && !c.nc.IsClosed() {
		c.nc.Close()
	}
	return nil
}

// IsConnected returns true if connected to NATS
func (c *Client) IsConnected() bool {
	return c.nc != nil && c.nc.IsConnected()
}

// PushAccountJWT pushes an account JWT to the NATS resolver
// The resolver listens on $SYS.REQ.CLAIMS.UPDATE for JWT updates
// This matches the behavior of `nsc push`
func (c *Client) PushAccountJWT(ctx context.Context, account *entities.Account) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to NATS")
	}

	// The subject for pushing account JWTs to the resolver
	// Using the same subject as nsc push command
	subject := "$SYS.REQ.CLAIMS.UPDATE"

	// Create context with timeout if not already set
	reqCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	// Push the JWT to the resolver
	// The resolver expects the JWT as the message payload
	msg, err := c.nc.RequestWithContext(reqCtx, subject, []byte(account.JWT))
	if err != nil {
		return fmt.Errorf("failed to push account JWT: %w", err)
	}

	// Check response - should be "+OK" or similar
	if len(msg.Data) > 0 {
		response := string(msg.Data)
		// NATS resolver typically returns "+OK" on success or "-ERR ..." on error
		if response[0] == '-' {
			return fmt.Errorf("resolver error: %s", response)
		}
	}

	return nil
}

// DeleteAccountJWT removes an account JWT from the NATS resolver
func (c *Client) DeleteAccountJWT(ctx context.Context, publicKey string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to NATS")
	}

	// The subject for deleting account JWTs from the resolver
	subject := fmt.Sprintf("$SYS.REQ.CLAIMS.DELETE.%s", publicKey)

	// Create context with timeout if not already set
	reqCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	msg, err := c.nc.RequestWithContext(reqCtx, subject, nil)
	if err != nil {
		return fmt.Errorf("failed to delete account JWT: %w", err)
	}

	if len(msg.Data) > 0 {
		response := string(msg.Data)
		if response[0] == '-' {
			return fmt.Errorf("resolver error: %s", response)
		}
	}

	return nil
}

// GetAccountJWT retrieves an account JWT from the NATS resolver
func (c *Client) GetAccountJWT(ctx context.Context, publicKey string) (string, error) {
	if !c.IsConnected() {
		return "", fmt.Errorf("not connected to NATS")
	}

	// The subject for getting account JWTs from the resolver
	subject := fmt.Sprintf("$SYS.REQ.ACCOUNT.%s.CLAIMS.LOOKUP", publicKey)

	// Create context with timeout if not already set
	reqCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	msg, err := c.nc.RequestWithContext(reqCtx, subject, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get account JWT: %w", err)
	}

	if len(msg.Data) == 0 {
		return "", fmt.Errorf("empty response from resolver")
	}

	response := string(msg.Data)
	if response[0] == '-' {
		return "", fmt.Errorf("resolver error: %s", response)
	}

	return response, nil
}

// Publish publishes a message to a subject
func (c *Client) Publish(subject string, data []byte) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to NATS")
	}
	return c.nc.Publish(subject, data)
}

// Request makes a request and waits for a response
func (c *Client) Request(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to NATS")
	}
	return c.nc.RequestWithContext(ctx, subject, data)
}
