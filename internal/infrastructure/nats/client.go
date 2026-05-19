package nats

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
func NewClientFromCreds(serverURLs []string, credsContent string, skipVerifyTLS bool) (*Client, error) {
	if len(serverURLs) == 0 {
		return nil, fmt.Errorf("at least one server URL is required")
	}

	opts := []nats.Option{
		nats.Timeout(10 * time.Second),
		nats.Name("NATS Identity Service"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
	}

	// Skip TLS verification if requested
	if skipVerifyTLS {
		opts = append(opts, nats.Secure(&tls.Config{InsecureSkipVerify: true}))
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
	var startIdx int
	if idx := find(credsContent, jwtStart); idx >= 0 {
		startIdx = idx + jwtStartIdx
	} else {
		return "", "", fmt.Errorf("JWT start marker not found")
	}

	var endIdx int
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
	if idx := find(credsContent, seedStart); idx >= 0 {
		startIdx = idx + seedStartIdx
	} else {
		return "", "", fmt.Errorf("seed start marker not found")
	}

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

// DeleteAccountJWT removes account JWTs from the NATS resolver
// The deleteClaimJWT must be an operator-signed generic claim JWT with an "accounts" field
// containing the list of account public keys to delete
func (c *Client) DeleteAccountJWT(ctx context.Context, deleteClaimJWT string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to NATS")
	}

	// The subject for deleting account JWTs from the resolver
	subject := "$SYS.REQ.CLAIMS.DELETE"

	// Create context with timeout if not already set
	reqCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	msg, err := c.nc.RequestWithContext(reqCtx, subject, []byte(deleteClaimJWT))
	if err != nil {
		return fmt.Errorf("failed to delete account JWT: %w", err)
	}

	if len(msg.Data) > 0 {
		response := string(msg.Data)
		// Check for error in old format
		if response[0] == '-' {
			return fmt.Errorf("resolver error: %s", response)
		}
		// Check for JSON error response
		if response[0] == '{' {
			var jsonResp struct {
				Error *struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"error,omitempty"`
			}
			if err := json.Unmarshal(msg.Data, &jsonResp); err == nil && jsonResp.Error != nil {
				return fmt.Errorf("resolver error %d: %s", jsonResp.Error.Code, jsonResp.Error.Description)
			}
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

// claimsListResponse represents the JSON response from $SYS.REQ.CLAIMS.LIST
type claimsListResponse struct {
	Data   []string `json:"data"`
	Error  *struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error,omitempty"`
}

// ProbeResolver checks whether a JWT resolver is listening on this NATS server by
// sending a CLAIMS.LIST request with a short timeout. Returns nil if the resolver
// responded (regardless of whether any accounts are stored), or an error otherwise.
// A "no responders" error means the server has no resolver configured (open mode
// or misconfigured), which is the most common failure mode operators hit.
func (c *Client) ProbeResolver(ctx context.Context) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to NATS")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := c.nc.RequestWithContext(probeCtx, "$SYS.REQ.CLAIMS.LIST", nil)
	if err != nil {
		return fmt.Errorf("JWT resolver did not respond on $SYS.REQ.CLAIMS.LIST: %w", err)
	}
	return nil
}

// ListAccountsFromResolver retrieves the list of account public keys from the NATS resolver
// Returns a list of account public keys that are currently stored in the resolver
func (c *Client) ListAccountsFromResolver(ctx context.Context) ([]string, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to NATS")
	}

	// The subject for listing account JWTs from the resolver
	subject := "$SYS.REQ.CLAIMS.LIST"

	// Create context with timeout if not already set
	reqCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	msg, err := c.nc.RequestWithContext(reqCtx, subject, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts from resolver: %w", err)
	}

	if len(msg.Data) == 0 {
		return []string{}, nil
	}

	response := string(msg.Data)

	// Check for old-style error format
	if response[0] == '-' {
		return nil, fmt.Errorf("resolver error: %s", response)
	}

	// Try to parse as JSON (newer NATS versions return JSON)
	if response[0] == '{' {
		var jsonResp claimsListResponse
		if err := parseJSON(msg.Data, &jsonResp); err != nil {
			return nil, fmt.Errorf("failed to parse claims list response: %w", err)
		}
		if jsonResp.Error != nil {
			return nil, fmt.Errorf("resolver error %d: %s", jsonResp.Error.Code, jsonResp.Error.Description)
		}
		return jsonResp.Data, nil
	}

	// Fallback to old newline-separated format
	var publicKeys []string
	for _, line := range splitLines(response) {
		line = trim(line)
		if line != "" {
			publicKeys = append(publicKeys, line)
		}
	}

	return publicKeys, nil
}

// parseJSON parses JSON data into a target struct
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// splitLines splits a string by newlines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
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

// JetStreamAccountInfo is a minimal local mirror of nats-server's monitor.JSInfo
// + JetStreamStats. We intentionally do NOT import nats-server/v2 (a heavy module
// that drags in the full server) just to decode a system-account reply.
//
// Field set is the cluster-wide aggregate for one account, as returned by
// $SYS.REQ.ACCOUNT.<accountPublicKey>.JSZ. Add fields here as we need them;
// the JSON decoder ignores unknown fields.
type JetStreamAccountInfo struct {
	Memory          uint64 `json:"memory"`
	Storage         uint64 `json:"storage"`
	ReservedMemory  uint64 `json:"reserved_memory"`
	ReservedStorage uint64 `json:"reserved_storage"`
	Streams         int    `json:"streams"`
	Consumers       int    `json:"consumers"`
	APITotal        uint64 `json:"-"`
	APIErrors       uint64 `json:"-"`
	Disabled        bool   `json:"disabled,omitempty"`
}

// Sentinel errors returned by QueryAccountJetStreamInfo so the service layer
// can map them to the public JetStreamProbeStatus enum.
var (
	// ErrJetStreamUnreachable: no NATS server responded, or the server has
	// no JetStream subsystem at all. Includes nats.ErrNoResponders and
	// context-deadline cases.
	ErrJetStreamUnreachable = errors.New("jetstream: cluster unreachable or no jetstream subsystem")

	// ErrJetStreamAccountNotFound: the cluster has no record of the account
	// (no JWT pushed, or JS state never initialised for it).
	ErrJetStreamAccountNotFound = errors.New("jetstream: account not found on cluster")

	// ErrJetStreamNotEnabled: cluster knows the account but JS is disabled
	// for it (limits set to zero, or response carries disabled=true).
	ErrJetStreamNotEnabled = errors.New("jetstream: not enabled for account on cluster")
)

// jszEnvelope mirrors NATS's AccountDetail (the actual payload for
// $SYS.REQ.ACCOUNT.<id>.JSZ — the request handler returns JszAccount, not Jsz).
// AccountDetail embeds JetStreamStats at the top level (memory/storage/api),
// plus an optional stream_detail array — populated when the request body sets
// {"streams": true}. We count the array length to derive the per-account stream
// count, and sum per-stream consumer counts from each stream's state.
type jszEnvelope struct {
	// embedded JetStreamStats
	Memory          uint64 `json:"memory"`
	Storage         uint64 `json:"storage"`
	ReservedMemory  uint64 `json:"reserved_memory"`
	ReservedStorage uint64 `json:"reserved_storage"`
	API             struct {
		Total  uint64 `json:"total"`
		Errors uint64 `json:"errors"`
	} `json:"api"`
	// stream_detail is populated only when the request sets streams=true.
	StreamDetail []jszStreamDetail `json:"stream_detail,omitempty"`
	Disabled     bool              `json:"disabled,omitempty"`
}

// jszStreamDetail is the slice of StreamDetail we actually care about — name
// plus consumer count from state. Everything else (config, cluster info, raft
// group) is ignored at decode time.
type jszStreamDetail struct {
	Name  string `json:"name"`
	State struct {
		Consumers int `json:"consumer_count"`
	} `json:"state"`
}

type jszResponse struct {
	Server *struct {
		ID string `json:"id"`
	} `json:"server,omitempty"`
	Data  *jszEnvelope `json:"data,omitempty"`
	Error *struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error,omitempty"`
}

// QueryAccountJetStreamInfo returns live JetStream usage for the given account
// public key by sending $SYS.REQ.ACCOUNT.<key>.JSZ. The connecting user must
// be on the system account (which the cluster's stored system-user creds are).
// The 3-second internal timeout matches the existing ProbeResolver/health-check
// pattern; the caller's ctx deadline still takes precedence if shorter.
func (c *Client) QueryAccountJetStreamInfo(ctx context.Context, accountPublicKey string) (*JetStreamAccountInfo, error) {
	if !c.IsConnected() {
		return nil, ErrJetStreamUnreachable
	}
	if accountPublicKey == "" {
		return nil, fmt.Errorf("accountPublicKey is required")
	}

	reqCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}

	subject := fmt.Sprintf("$SYS.REQ.ACCOUNT.%s.JSZ", accountPublicKey)

	// Request body asks for the per-stream array so we can derive the
	// account's stream count and (by summing per-stream state.consumer_count)
	// its consumer total. Without streams=true the response carries only
	// JetStreamStats (bytes), which is most of the picture but leaves the
	// "N streams / N consumers" headline unpopulated.
	body := []byte(`{"streams":true}`)
	msg, err := c.nc.RequestWithContext(reqCtx, subject, body)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrJetStreamUnreachable
		}
		return nil, fmt.Errorf("jetstream: query failed: %w", err)
	}
	if len(msg.Data) == 0 {
		return nil, ErrJetStreamUnreachable
	}

	var resp jszResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("jetstream: decode response: %w", err)
	}
	if resp.Error != nil {
		desc := strings.ToLower(resp.Error.Description)
		if resp.Error.Code == 404 || strings.Contains(desc, "not found") || strings.Contains(desc, "no account") {
			return nil, ErrJetStreamAccountNotFound
		}
		if strings.Contains(desc, "jetstream not enabled") || strings.Contains(desc, "not enabled for jetstream") {
			return nil, ErrJetStreamNotEnabled
		}
		return nil, fmt.Errorf("jetstream: resolver error %d: %s", resp.Error.Code, resp.Error.Description)
	}
	if resp.Data == nil {
		// No data and no error: treat as unreachable rather than silently
		// returning zeros — operator should see something is off.
		return nil, ErrJetStreamUnreachable
	}
	if resp.Data.Disabled {
		return nil, ErrJetStreamNotEnabled
	}

	consumers := 0
	for _, sd := range resp.Data.StreamDetail {
		consumers += sd.State.Consumers
	}

	return &JetStreamAccountInfo{
		Memory:          resp.Data.Memory,
		Storage:         resp.Data.Storage,
		ReservedMemory:  resp.Data.ReservedMemory,
		ReservedStorage: resp.Data.ReservedStorage,
		Streams:         len(resp.Data.StreamDetail),
		Consumers:       consumers,
		APITotal:        resp.Data.API.Total,
		APIErrors:       resp.Data.API.Errors,
	}, nil
}
