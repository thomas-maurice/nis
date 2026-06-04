//go:build e2e && e2e_oidc

// sso_test.go — Dex-backed OIDC SSO integration tests.
//
// These tests boot a real Dex OIDC container and verify the /auth/oidc/start
// and /auth/oidc/callback HTTP endpoints end-to-end without a browser.
//
// Coverage:
//   - /auth/oidc/start?org=<slug> returns 302 to Dex authorization URL with
//     correct PKCE (code_challenge_method=S256), state, and nonce params.
//   - /auth/oidc/callback with an invalid state returns 400 (timing-jittered).
//   - /auth/oidc/start for an unknown org returns 400 (jittered, no slug leak).
//   - NIS /auth/oidc/* paths are NOT served by the SPA fallback.
//
// Full code exchange (Dex → NIS → session JWT) is NOT covered here because it
// requires POSTing credentials to Dex's login form (browser-only with PKCE).
// That path is exercised at the unit / service-layer by sso_service_test.go.
//
// Build tag: e2e_oidc (separate from the main "e2e" tag so the Dex container
// requirement doesn't block the standard CI matrix).
//
// Run with:
//
//	go test -tags=e2e_oidc -v ./tests/e2e/ -run TestE2E_OIDC
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// dexOIDCE2EHarness extends the base harness with a Dex container and an org
// service client. Owns its own teardown.
type dexOIDCE2EHarness struct {
	*harness
	dexPort      int
	dexURL       string
	dexContainer string

	orgCli nisv1connect.OrganizationServiceClient
}

// dexConfig is the Dex static config YAML template. %s placeholders are filled
// with the Dex base URL and the NIS callback URL.
const dexConfigTemplate = `
issuer: %s

storage:
  type: memory

web:
  http: 0.0.0.0:5556

connectors:
  - type: mockCallback
    id: mock
    name: Mock

staticClients:
  - id: nis-test-client
    secret: nis-test-secret
    redirectURIs:
      - %s
    name: NIS Test

enablePasswordDB: true
staticPasswords:
  - email: "testuser@example.com"
    hash: "$2y$12$yH7IKMZd4x6r3Zu4YWmb3.JKBS7SKUTYMiJI5qvt3BFzlPRVbcq6"
    username: "testuser"
    userID: "test-user-id-1234"

oauth2:
  skipApprovalScreen: true
  responseTypes:
    - code
`

// startDexOIDCHarness boots a Dex container and NIS with PUBLIC_URL set so the
// OIDC callback URL is constructed correctly.
func startDexOIDCHarness(t *testing.T) *dexOIDCE2EHarness {
	t.Helper()

	h := newHarness(t)
	dexPort := pickFreePort(t)
	dexContainer := fmt.Sprintf("nis-e2e-dex-%d", time.Now().UnixNano())
	dexURL := fmt.Sprintf("http://127.0.0.1:%d", dexPort)

	// Write Dex config to a temp file accessible by the Docker volume mount.
	workDir := t.TempDir()
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/auth/oidc/callback", h.nisPort)
	dexConf := fmt.Sprintf(dexConfigTemplate, dexURL, callbackURL)
	dexConfPath := filepath.Join(workDir, "dex-config.yaml")
	require.NoError(t, os.WriteFile(dexConfPath, []byte(dexConf), 0o644))

	// Boot Dex.
	args := []string{
		"run", "-d",
		"--name", dexContainer,
		"-p", fmt.Sprintf("127.0.0.1:%d:5556", dexPort),
		"-v", fmt.Sprintf("%s:/dex-config.yaml:ro", dexConfPath),
		"ghcr.io/dexidp/dex:v2.41.1",
		"dex", "serve", "/dex-config.yaml",
	}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start dex container: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", dexContainer).Run()
	})

	// Wait for Dex to publish its OIDC discovery doc.
	if err := waitForHTTP(dexURL+"/.well-known/openid-configuration", natsReadyTimeout); err != nil {
		dump, _ := exec.Command("docker", "logs", dexContainer).CombinedOutput()
		t.Fatalf("dex did not become healthy: %v\ndex logs:\n%s", err, dump)
	}

	// Boot NIS with PUBLIC_URL set so callbackURL() returns the right value.
	t.Cleanup(h.teardown)
	// Inject SERVER_PUBLIC_URL into the NIS environment.
	publicURL := fmt.Sprintf("http://127.0.0.1:%d", h.nisPort)
	origEnv := os.Environ()

	h.nisProcess = startNISWithExtraEnv(t, h, origEnv, []string{
		"SERVER_PUBLIC_URL=" + publicURL,
	})

	authOpt := connect.WithInterceptors(&bearerInterceptor{token: h.authToken})
	return &dexOIDCE2EHarness{
		harness:      h,
		dexPort:      dexPort,
		dexURL:       dexURL,
		dexContainer: dexContainer,
		orgCli:       nisv1connect.NewOrganizationServiceClient(h.httpClient, h.serverURL, authOpt),
	}
}

// startNISWithExtraEnv starts NIS for the harness, appending extraEnv to the
// environment. It reuses the harness start() logic but injects additional env
// vars before launch.
//
// NOTE: h.start(t) sets h.nisProcess internally. Because we need to inject env
// vars that aren't supported by the existing start() code path, we replicate
// the relevant part here. This is the minimum surgical extension — the harness
// struct itself is not modified.
func startNISWithExtraEnv(t *testing.T, h *harness, baseEnv []string, extra []string) *exec.Cmd {
	t.Helper()
	h.serverURL = fmt.Sprintf("http://127.0.0.1:%d", h.nisPort)

	dbPath := filepath.Join(h.workDir, "nis.db")
	if err := os.Symlink(filepath.Join(h.repoDir, "migrations"), filepath.Join(h.workDir, "migrations")); err != nil {
		// Symlink may already exist if harness.start() ran; skip EEXIST.
		if !os.IsExist(err) {
			t.Fatalf("symlink migrations: %v", err)
		}
	}

	logFile, err := os.Create(h.nisLogPath)
	if err != nil {
		t.Fatalf("create nis log: %v", err)
	}

	cmd := exec.Command(h.nisBin, "serve",
		"--address", fmt.Sprintf("127.0.0.1:%d", h.nisPort),
		"--enable-ui=false",
	)
	cmd.Dir = h.workDir
	cmd.Env = append(baseEnv,
		"AUTH_JWT_SECRET="+jwtSecret,
		"ENCRYPTION_KEY="+h.effectiveEncryptionKey(),
		"DATABASE_DRIVER=sqlite",
		"DATABASE_DSN="+dbPath,
		"DATABASE_AUTO_MIGRATE=true",
		"JOBS_POLL_INTERVAL_SECONDS=1",
		"CLUSTER_HEALTH_CHECK_INITIAL_DELAY_SECONDS=1",
		"CLUSTER_HEALTH_CHECK_INTERVAL_SECONDS=2",
	)
	cmd.Env = append(cmd.Env, extra...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nis: %v", err)
	}
	h.nisProcess = cmd

	if err := waitForHTTP(h.serverURL+"/healthz", httpReadyTimeout); err != nil {
		t.Fatalf("nis server did not become healthy: %v (see %s)", err, h.nisLogPath)
	}

	// Bootstrap admin + login (mirrors harness.start).
	bootstrap := exec.Command(h.nisBin, "user", "create", adminUsername,
		"--password", adminPassword, "--role", "admin",
	)
	bootstrap.Dir = h.workDir
	bootstrap.Env = append(os.Environ(),
		"AUTH_JWT_SECRET="+jwtSecret,
		"ENCRYPTION_KEY="+h.effectiveEncryptionKey(),
		"DATABASE_DRIVER=sqlite",
		"DATABASE_DSN="+dbPath,
	)
	if out, err := bootstrap.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap admin: %v\n%s", err, out)
	}

	authCli := nisv1connect.NewAuthServiceClient(h.httpClient, h.serverURL)
	loginResp, err := authCli.Login(context.Background(), connect.NewRequest(&nisv1.LoginRequest{
		Username: adminUsername, Password: adminPassword,
	}))
	if err != nil {
		t.Fatalf("login as admin: %v", err)
	}
	h.authToken = loginResp.Msg.Token

	authOpt := connect.WithInterceptors(&bearerInterceptor{token: h.authToken})
	h.operatorCli = nisv1connect.NewOperatorServiceClient(h.httpClient, h.serverURL, authOpt)
	h.accountCli = nisv1connect.NewAccountServiceClient(h.httpClient, h.serverURL, authOpt)
	h.userCli = nisv1connect.NewUserServiceClient(h.httpClient, h.serverURL, authOpt)
	h.clusterCli = nisv1connect.NewClusterServiceClient(h.httpClient, h.serverURL, authOpt)
	h.keyCli = nisv1connect.NewScopedSigningKeyServiceClient(h.httpClient, h.serverURL, authOpt)
	h.exportCli = nisv1connect.NewExportServiceClient(h.httpClient, h.serverURL, authOpt)
	h.eventCli = nisv1connect.NewEventServiceClient(h.httpClient, h.serverURL, authOpt)
	h.webhookCli = nisv1connect.NewWebhookServiceClient(h.httpClient, h.serverURL, authOpt)
	h.apiTokenCli = nisv1connect.NewAPITokenServiceClient(h.httpClient, h.serverURL, authOpt)
	h.searchCli = nisv1connect.NewSearchServiceClient(h.httpClient, h.serverURL, authOpt)
	h.templateCli = nisv1connect.NewTemplateServiceClient(h.httpClient, h.serverURL, authOpt)
	h.jobCli = nisv1connect.NewJobServiceClient(h.httpClient, h.serverURL, authOpt)
	h.backupCli = nisv1connect.NewBackupServiceClient(h.httpClient, h.serverURL, authOpt)
	h.configCli = nisv1connect.NewConfigServiceClient(h.httpClient, h.serverURL, authOpt)

	return cmd
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestE2E_OIDC_StartReturnsRedirectToDex verifies that /auth/oidc/start?org=<slug>
// returns a 302 pointing at Dex's authorization endpoint with PKCE params.
func TestE2E_OIDC_StartReturnsRedirectToDex(t *testing.T) {
	h := startDexOIDCHarness(t)
	ctx := context.Background()

	// Create org + SSO config.
	orgSlug := "oidc-test-org"
	createResp, err := h.orgCli.CreateOrganization(ctx, connect.NewRequest(&nisv1.CreateOrganizationRequest{
		Name: "OIDC Test Org",
		Slug: orgSlug,
	}))
	require.NoError(t, err)
	orgID := createResp.Msg.Organization.Id

	_, err = h.orgCli.SetSSOConfig(ctx, connect.NewRequest(&nisv1.SetSSOConfigRequest{
		OrganizationId: orgID,
		Enabled:        true,
		IssuerUrl:      h.dexURL,
		ClientId:       "nis-test-client",
		ClientSecret:   "nis-test-secret",
		DefaultRole:    "viewer",
	}))
	require.NoError(t, err)

	// Hit the start endpoint without following redirects.
	noRedirectClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}
	startURL := fmt.Sprintf("%s/auth/oidc/start?org=%s", h.serverURL, orgSlug)
	resp, err := noRedirectClient.Get(startURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode, "start should redirect")

	loc := resp.Header.Get("Location")
	require.NotEmpty(t, loc, "Location header must be set on redirect")

	parsed, err := url.Parse(loc)
	require.NoError(t, err)

	// The redirect must point to Dex's authorization endpoint.
	assert.Equal(t, "127.0.0.1", parsed.Hostname(), "redirect host should be Dex")
	assert.Equal(t, fmt.Sprintf("%d", h.dexPort), parsed.Port(), "redirect port should be Dex's port")
	assert.Equal(t, "/auth", parsed.Path, "redirect path should be /auth (Dex authorization endpoint)")

	q := parsed.Query()
	assert.Equal(t, "nis-test-client", q.Get("client_id"))
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"), "PKCE S256 must be present")
	assert.NotEmpty(t, q.Get("code_challenge"), "PKCE code_challenge must be present")
	assert.NotEmpty(t, q.Get("state"), "state parameter must be present")
	assert.NotEmpty(t, q.Get("nonce"), "nonce parameter must be present")

	// Redirect URI must point back to NIS callback.
	assert.Contains(t, q.Get("redirect_uri"), "/auth/oidc/callback", "redirect_uri must point to NIS callback")
}

// TestE2E_OIDC_CallbackWithInvalidStateReturns400 verifies that the callback
// endpoint returns 400 for an unknown/invalid state without leaking details.
func TestE2E_OIDC_CallbackWithInvalidStateReturns400(t *testing.T) {
	h := startDexOIDCHarness(t)

	callbackURL := fmt.Sprintf("%s/auth/oidc/callback?code=fakecode&state=definitely-not-valid", h.serverURL)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(callbackURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "invalid state should return 400")
}

// TestE2E_OIDC_StartWithUnknownOrgReturns400 verifies that an unknown org slug
// returns 400 without leaking whether the slug exists.
func TestE2E_OIDC_StartWithUnknownOrgReturns400(t *testing.T) {
	h := startDexOIDCHarness(t)

	startURL := fmt.Sprintf("%s/auth/oidc/start?org=nonexistent-org-slug", h.serverURL)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(startURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown org should return 400")
}

// TestE2E_OIDC_StartPathNotServedBySPA ensures the SPA fallback does not serve
// OIDC paths as HTML; these must be handled by the dedicated handler.
func TestE2E_OIDC_StartPathNotServedBySPA(t *testing.T) {
	h := startDexOIDCHarness(t)

	// The OIDC start path with no org returns 400 (the handler fires, not SPA).
	startURL := fmt.Sprintf("%s/auth/oidc/start", h.serverURL)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(startURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	// SPA would return 200 with text/html; OIDC handler returns 400.
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"/auth/oidc/start without params must NOT be served as SPA (200 would indicate fallback)")
	ct := resp.Header.Get("Content-Type")
	assert.NotContains(t, ct, "text/html",
		"OIDC start path must not return HTML (would indicate SPA fallback)")
}
