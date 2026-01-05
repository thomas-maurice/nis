package client

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// Client wraps all NIS gRPC service clients
type Client struct {
	serverURL string
	token     string
	httpClient *http.Client

	// Service clients
	Operator          nisv1connect.OperatorServiceClient
	Account           nisv1connect.AccountServiceClient
	User              nisv1connect.UserServiceClient
	ScopedSigningKey  nisv1connect.ScopedSigningKeyServiceClient
	Cluster           nisv1connect.ClusterServiceClient
	Auth              nisv1connect.AuthServiceClient
	Export            nisv1connect.ExportServiceClient
}

// NewClient creates a new NIS client with authentication
func NewClient(serverURL, token string) (*Client, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	// Create HTTP client with auth interceptor
	httpClient := &http.Client{
		Transport: &authTransport{
			token:     token,
			transport: http.DefaultTransport,
		},
	}

	client := &Client{
		serverURL:  serverURL,
		token:      token,
		httpClient: httpClient,
	}

	// Initialize service clients
	client.Operator = nisv1connect.NewOperatorServiceClient(httpClient, serverURL)
	client.Account = nisv1connect.NewAccountServiceClient(httpClient, serverURL)
	client.User = nisv1connect.NewUserServiceClient(httpClient, serverURL)
	client.ScopedSigningKey = nisv1connect.NewScopedSigningKeyServiceClient(httpClient, serverURL)
	client.Cluster = nisv1connect.NewClusterServiceClient(httpClient, serverURL)
	client.Auth = nisv1connect.NewAuthServiceClient(httpClient, serverURL)
	client.Export = nisv1connect.NewExportServiceClient(httpClient, serverURL)

	return client, nil
}

// Close closes the client connection
func (c *Client) Close() error {
	// ConnectRPC clients don't need explicit closing
	// The HTTP client will be garbage collected
	return nil
}

// authTransport is an http.RoundTripper that adds authentication headers
type authTransport struct {
	token     string
	transport http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Add authorization header if token is present
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}

	// Use the underlying transport
	return t.transport.RoundTrip(req)
}

// Login authenticates with the NIS server and returns a token
func Login(serverURL, username, password string) (string, error) {
	if serverURL == "" {
		return "", fmt.Errorf("server URL is required")
	}
	if username == "" {
		return "", fmt.Errorf("username is required")
	}
	if password == "" {
		return "", fmt.Errorf("password is required")
	}

	// Create a temporary client without auth for login
	httpClient := &http.Client{}
	authClient := nisv1connect.NewAuthServiceClient(httpClient, serverURL)

	// Attempt login
	req := connect.NewRequest(&nisv1.LoginRequest{
		Username: username,
		Password: password,
	})

	resp, err := authClient.Login(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("login failed: %w", err)
	}

	return resp.Msg.Token, nil
}
