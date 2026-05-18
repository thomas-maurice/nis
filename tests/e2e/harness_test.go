//go:build e2e

// Package e2e is the end-to-end test suite for NIS. Each scenario file in this
// package (lifecycle_test.go, nats_live_test.go, export_test.go, ...) drives a
// fresh NIS process and — when needed — a real NATS server in Docker against a
// freshly bootstrapped identity tree. The shared harness lives here.
//
// Run with:    make test-e2e
// Or:          go test -tags=e2e -v ./tests/e2e/...
//
// Requirements:
//   - docker daemon running (for the NATS container; tests that don't touch
//     NATS skip the docker probe entirely).
//   - free TCP ports (the harness picks unused ports for NIS and NATS).
package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/nats-io/nats.go"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

const (
	adminUsername = "e2e-admin"
	adminPassword = "e2e-admin-password-do-not-use-elsewhere"
	jwtSecret     = "e2e-test-jwt-secret-min-32-bytes-min-32-bytes"
	encryptionKey = "e2e-test-encryption-key-32bytes!"

	httpReadyTimeout = 30 * time.Second
	natsReadyTimeout = 30 * time.Second
)

// harness owns one NIS process, optionally one NATS container, and the typed
// Connect-RPC clients authenticated as the bootstrapped admin user. One harness
// per top-level test — never share across tests.
type harness struct {
	t       *testing.T
	workDir string
	repoDir string
	nisBin  string

	// encryptionKeyOverride lets a test boot a NIS instance with a non-default
	// encryption key — used by the wrong-key import test to prove that an
	// encrypted export from server A cannot be silently consumed by server B.
	// Empty string ⇒ fall back to the package-level encryptionKey constant.
	encryptionKeyOverride string

	nisPort      int
	natsPort     int
	natsMgmtPort int

	serverURL string
	natsURL   string

	nisProcess    *exec.Cmd
	nisLogPath    string
	natsContainer string
	natsStarted   bool

	httpClient *http.Client
	authToken  string

	operatorCli nisv1connect.OperatorServiceClient
	accountCli  nisv1connect.AccountServiceClient
	userCli     nisv1connect.UserServiceClient
	clusterCli  nisv1connect.ClusterServiceClient
	keyCli      nisv1connect.ScopedSigningKeyServiceClient
	exportCli   nisv1connect.ExportServiceClient
	eventCli    nisv1connect.EventServiceClient
	webhookCli  nisv1connect.WebhookServiceClient
	apiTokenCli nisv1connect.APITokenServiceClient
}

// startStack is the canonical entry point for a test. It boots NIS, bootstraps
// the admin user, logs in, and registers teardown with t.Cleanup. NATS is NOT
// started here — call h.startNATSForOperator(t, operatorID) when a test needs a
// real JWT-authenticated NATS server.
func startStack(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	t.Cleanup(h.teardown)
	h.start(t)
	return h
}

// effectiveEncryptionKey returns the per-harness override if set, else the
// package-level default. Tests that need to compare keyspaces (e.g. wrong-key
// import) set encryptionKeyOverride before calling start().
func (h *harness) effectiveEncryptionKey() string {
	if h.encryptionKeyOverride != "" {
		return h.encryptionKeyOverride
	}
	return encryptionKey
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("e2e suite is POSIX-only")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not found in PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}

	repoDir, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	workDir := t.TempDir()
	nisBin, err := ensureNISBinary(repoDir, workDir)
	if err != nil {
		t.Fatalf("locate/build nis binary: %v", err)
	}

	return &harness{
		t:             t,
		workDir:       workDir,
		repoDir:       repoDir,
		nisBin:        nisBin,
		nisPort:       pickFreePort(t),
		natsPort:      pickFreePort(t),
		natsMgmtPort:  pickFreePort(t),
		nisLogPath:    filepath.Join(workDir, "nis.log"),
		natsContainer: fmt.Sprintf("nis-e2e-%d", time.Now().UnixNano()),
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (h *harness) start(t *testing.T) {
	t.Helper()
	h.serverURL = fmt.Sprintf("http://127.0.0.1:%d", h.nisPort)
	h.natsURL = fmt.Sprintf("nats://127.0.0.1:%d", h.natsPort)

	dbPath := filepath.Join(h.workDir, "nis.db")

	// goose Up reads migration .sql files off disk relative to cwd; the
	// repo-root `config.yaml` would also be auto-loaded if we cwd'd there and
	// override the DSN via `database.path`. Both problems disappear by running
	// the server from a clean workDir with a symlink to the migrations/
	// directory and no config.yaml in sight, so only flags+env are honored.
	if err := os.Symlink(filepath.Join(h.repoDir, "migrations"), filepath.Join(h.workDir, "migrations")); err != nil {
		t.Fatalf("symlink migrations: %v", err)
	}

	logFile, err := os.Create(h.nisLogPath)
	if err != nil {
		t.Fatalf("create nis log: %v", err)
	}
	h.nisProcess = exec.Command(h.nisBin, "serve",
		"--address", fmt.Sprintf("127.0.0.1:%d", h.nisPort),
		"--enable-ui=false",
		"--webhooks-poll-interval-seconds=1",
		"--webhooks-backoff-base-seconds=1",
		"--webhooks-backoff-cap-seconds=10",
	)
	h.nisProcess.Dir = h.workDir
	h.nisProcess.Env = append(os.Environ(),
		"AUTH_JWT_SECRET="+jwtSecret,
		"ENCRYPTION_KEY="+h.effectiveEncryptionKey(),
		"DATABASE_DRIVER=sqlite",
		"DATABASE_DSN="+dbPath,
		"DATABASE_AUTO_MIGRATE=true",
	)
	h.nisProcess.Stdout = logFile
	h.nisProcess.Stderr = logFile
	if err := h.nisProcess.Start(); err != nil {
		t.Fatalf("start nis: %v", err)
	}

	if err := waitForHTTP(h.serverURL+"/healthz", httpReadyTimeout); err != nil {
		t.Fatalf("nis server did not become healthy: %v (see %s)", err, h.nisLogPath)
	}

	// Now that tables exist, bootstrap the admin user via the CLI (which opens
	// its own DB connection — fine for SQLite under WAL mode). Use env vars to
	// override database config: the skill (§4d) documents that viper BindPFlag
	// defaults don't reliably override config.yaml; env vars work cleanly.
	bootstrap := exec.Command(h.nisBin, "user", "create", adminUsername,
		"--password", adminPassword,
		"--role", "admin",
	)
	bootstrap.Dir = h.workDir
	bootstrap.Env = append(os.Environ(),
		"AUTH_JWT_SECRET="+jwtSecret,
		"ENCRYPTION_KEY="+h.effectiveEncryptionKey(),
		"DATABASE_DRIVER=sqlite",
		"DATABASE_DSN="+dbPath,
	)
	if out, err := bootstrap.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap admin user: %v\n%s", err, out)
	}

	// Unauthenticated auth client for the login call.
	authCli := nisv1connect.NewAuthServiceClient(h.httpClient, h.serverURL)
	loginResp, err := authCli.Login(context.Background(), connect.NewRequest(&nisv1.LoginRequest{
		Username: adminUsername,
		Password: adminPassword,
	}))
	if err != nil {
		t.Fatalf("login as admin: %v", err)
	}
	h.authToken = loginResp.Msg.Token

	// Rebuild the typed clients with an auth interceptor so every call carries the bearer.
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
}

// startNATSForOperator pulls the NATS include config for operatorID from NIS,
// writes it to disk, and boots a JWT-authenticated NATS container against it.
// The container is torn down with the harness.
func (h *harness) startNATSForOperator(t *testing.T, operatorID string) {
	t.Helper()
	includeResp, err := h.operatorCli.GenerateInclude(context.Background(), connect.NewRequest(&nisv1.GenerateIncludeRequest{
		Id: operatorID,
	}))
	if err != nil {
		t.Fatalf("GenerateInclude: %v", err)
	}
	natsConfPath := filepath.Join(h.workDir, "nats-server.conf")
	if err := os.WriteFile(natsConfPath, []byte(includeResp.Msg.Config), 0o644); err != nil {
		t.Fatalf("write nats config: %v", err)
	}

	args := []string{
		"run", "-d",
		"--name", h.natsContainer,
		"-p", fmt.Sprintf("127.0.0.1:%d:4222", h.natsPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:8222", h.natsMgmtPort),
		"-v", fmt.Sprintf("%s:/nats-server.conf:ro", natsConfPath),
		"nats:2.10-alpine",
		"-c", "/nats-server.conf",
		"-m", "8222",
	}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start nats container: %v\n%s", err, out)
	}
	h.natsStarted = true

	mgmtURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", h.natsMgmtPort)
	if err := waitForHTTP(mgmtURL, natsReadyTimeout); err != nil {
		dump, _ := exec.Command("docker", "logs", h.natsContainer).CombinedOutput()
		t.Fatalf("nats did not become healthy: %v\nnats logs:\n%s", err, dump)
	}
}

// ---------------------------------------------------------------------------
// Identity-tree builders. Every helper here fatals the test on error so the
// scenario code stays focused on what's being asserted rather than how the
// fixture was assembled.
// ---------------------------------------------------------------------------

func (h *harness) createOperator(t *testing.T, name string) string {
	t.Helper()
	resp, err := h.operatorCli.CreateOperator(context.Background(), connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:        name,
		Description: "created by the e2e suite",
	}))
	if err != nil {
		t.Fatalf("CreateOperator(%s): %v", name, err)
	}
	return resp.Msg.Operator.Id
}

func (h *harness) createCluster(t *testing.T, operatorID, name string, serverURLs ...string) string {
	t.Helper()
	if len(serverURLs) == 0 {
		serverURLs = []string{h.natsURL}
	}
	resp, err := h.clusterCli.CreateCluster(context.Background(), connect.NewRequest(&nisv1.CreateClusterRequest{
		OperatorId: operatorID,
		Name:       name,
		ServerUrls: serverURLs,
	}))
	if err != nil {
		t.Fatalf("CreateCluster(%s): %v", name, err)
	}
	return resp.Msg.Cluster.Id
}

func (h *harness) createAccount(t *testing.T, operatorID, name string) string {
	t.Helper()
	resp, err := h.accountCli.CreateAccount(context.Background(), connect.NewRequest(&nisv1.CreateAccountRequest{
		OperatorId: operatorID,
		Name:       name,
	}))
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", name, err)
	}
	return resp.Msg.Account.Id
}

func (h *harness) createUser(t *testing.T, accountID, name string) string {
	t.Helper()
	resp, err := h.userCli.CreateUser(context.Background(), connect.NewRequest(&nisv1.CreateUserRequest{
		AccountId: accountID,
		Name:      name,
	}))
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return resp.Msg.User.Id
}

func (h *harness) createScopedUser(t *testing.T, accountID, name, scopedKeyID string) string {
	t.Helper()
	resp, err := h.userCli.CreateUser(context.Background(), connect.NewRequest(&nisv1.CreateUserRequest{
		AccountId:          accountID,
		Name:               name,
		ScopedSigningKeyId: scopedKeyID,
	}))
	if err != nil {
		t.Fatalf("CreateUser(%s, scoped=%s): %v", name, scopedKeyID, err)
	}
	return resp.Msg.User.Id
}

func (h *harness) createScopedKey(t *testing.T, accountID, name string, perms *nisv1.UserPermissions) string {
	t.Helper()
	resp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId:   accountID,
		Name:        name,
		Permissions: perms,
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey(%s): %v", name, err)
	}
	return resp.Msg.Key.Id
}

func (h *harness) regenerateUserCredentials(t *testing.T, userID string) {
	t.Helper()
	if _, err := h.userCli.RegenerateUserCredentials(context.Background(), connect.NewRequest(&nisv1.RegenerateUserCredentialsRequest{
		Id: userID,
	})); err != nil {
		t.Fatalf("RegenerateUserCredentials(%s): %v", userID, err)
	}
}

func (h *harness) syncCluster(t *testing.T, clusterID string) {
	t.Helper()
	if _, err := h.clusterCli.SyncCluster(context.Background(), connect.NewRequest(&nisv1.SyncClusterRequest{
		Id: clusterID,
	})); err != nil {
		t.Fatalf("SyncCluster(%s): %v", clusterID, err)
	}
}

// fetchUserCreds writes the user's .creds blob into the harness workDir under
// `<name>.creds` and returns the path. Tests pass this to nats.UserCredentials.
func (h *harness) fetchUserCreds(t *testing.T, userID, name string) string {
	t.Helper()
	resp, err := h.userCli.GetUserCredentials(context.Background(), connect.NewRequest(&nisv1.GetUserCredentialsRequest{
		Id: userID,
	}))
	if err != nil {
		t.Fatalf("GetUserCredentials(%s): %v", name, err)
	}
	credsPath := filepath.Join(h.workDir, name+".creds")
	if err := os.WriteFile(credsPath, []byte(resp.Msg.Credentials), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	return credsPath
}

// stack bundles the IDs of a "standard" operator → cluster → account → user
// tree synced once against a live NATS server. Use bootStandardStack when a
// test wants to skip past the boilerplate and assert on NATS-side behavior.
type stack struct {
	operatorID  string
	clusterID   string
	accountID   string
	userID      string
	credsPath   string
	userName    string
	accountName string
}

// bootStandardStack creates an operator, boots NATS authed against it, creates
// a cluster + account + user, syncs, and fetches the user's creds. Returns the
// IDs so the test can mutate further or assert directly.
func (h *harness) bootStandardStack(t *testing.T, prefix string) stack {
	t.Helper()
	operatorID := h.createOperator(t, prefix+"-operator")
	h.startNATSForOperator(t, operatorID)
	clusterID := h.createCluster(t, operatorID, prefix+"-cluster")
	accountID := h.createAccount(t, operatorID, prefix+"-account")
	userName := prefix + "-user"
	userID := h.createUser(t, accountID, userName)
	h.syncCluster(t, clusterID)
	return stack{
		operatorID:  operatorID,
		clusterID:   clusterID,
		accountID:   accountID,
		userID:      userID,
		credsPath:   h.fetchUserCreds(t, userID, userName),
		userName:    userName,
		accountName: prefix + "-account",
	}
}

// clientSet bundles the typed Connect-RPC clients for one authenticated
// identity. The harness's own *Cli fields are the admin set; tests that need
// to act as a non-admin call h.loginAs and use the returned clientSet.
type clientSet struct {
	token       string
	operatorCli nisv1connect.OperatorServiceClient
	accountCli  nisv1connect.AccountServiceClient
	userCli     nisv1connect.UserServiceClient
	clusterCli  nisv1connect.ClusterServiceClient
	keyCli      nisv1connect.ScopedSigningKeyServiceClient
	exportCli   nisv1connect.ExportServiceClient
	authCli     nisv1connect.AuthServiceClient
	eventCli    nisv1connect.EventServiceClient
	webhookCli  nisv1connect.WebhookServiceClient
	apiTokenCli nisv1connect.APITokenServiceClient
}

// loginAs authenticates as username/password and returns a clientSet whose
// requests carry that user's bearer token. Used by the RBAC tests to drive
// the API as a non-admin and verify the permission boundary holds.
func (h *harness) loginAs(t *testing.T, username, password string) clientSet {
	t.Helper()
	authCli := nisv1connect.NewAuthServiceClient(h.httpClient, h.serverURL)
	loginResp, err := authCli.Login(context.Background(), connect.NewRequest(&nisv1.LoginRequest{
		Username: username,
		Password: password,
	}))
	if err != nil {
		t.Fatalf("Login(%s): %v", username, err)
	}
	authOpt := connect.WithInterceptors(&bearerInterceptor{token: loginResp.Msg.Token})
	return clientSet{
		token:       loginResp.Msg.Token,
		operatorCli: nisv1connect.NewOperatorServiceClient(h.httpClient, h.serverURL, authOpt),
		accountCli:  nisv1connect.NewAccountServiceClient(h.httpClient, h.serverURL, authOpt),
		userCli:     nisv1connect.NewUserServiceClient(h.httpClient, h.serverURL, authOpt),
		clusterCli:  nisv1connect.NewClusterServiceClient(h.httpClient, h.serverURL, authOpt),
		keyCli:      nisv1connect.NewScopedSigningKeyServiceClient(h.httpClient, h.serverURL, authOpt),
		exportCli:   nisv1connect.NewExportServiceClient(h.httpClient, h.serverURL, authOpt),
		authCli:     nisv1connect.NewAuthServiceClient(h.httpClient, h.serverURL, authOpt),
		eventCli:    nisv1connect.NewEventServiceClient(h.httpClient, h.serverURL, authOpt),
		webhookCli:  nisv1connect.NewWebhookServiceClient(h.httpClient, h.serverURL, authOpt),
		apiTokenCli: nisv1connect.NewAPITokenServiceClient(h.httpClient, h.serverURL, authOpt),
	}
}

func (h *harness) teardown() {
	if h.nisProcess != nil && h.nisProcess.Process != nil {
		_ = h.nisProcess.Process.Kill()
		_, _ = h.nisProcess.Process.Wait()
	}
	if h.natsStarted && h.natsContainer != "" {
		_ = exec.Command("docker", "rm", "-f", h.natsContainer).Run()
	}
	if h.t.Failed() {
		if b, err := os.ReadFile(h.nisLogPath); err == nil {
			h.t.Logf("=== nis.log ===\n%s", b)
		}
	}
}

// ---------------------------------------------------------------------------
// Connect-RPC bearer-token interceptor
// ---------------------------------------------------------------------------

type bearerInterceptor struct{ token string }

func (b *bearerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+b.token)
		return next(ctx, req)
	}
}

func (b *bearerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set("Authorization", "Bearer "+b.token)
		return conn
	}
}

func (b *bearerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// ---------------------------------------------------------------------------
// Process / network helpers
// ---------------------------------------------------------------------------

func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found from %s", cwd)
}

// ensureNISBinary returns a path to a usable `nis` binary. If `./bin/nis` exists in the
// repo it is reused; otherwise the binary is freshly built into workDir.
func ensureNISBinary(repoDir, workDir string) (string, error) {
	candidate := filepath.Join(repoDir, "bin", "nis")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, nil
	}
	out := filepath.Join(workDir, "nis")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/nis")
	cmd.Dir = repoDir
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/nis: %w\n%s", err, b)
	}
	return out, nil
}

func waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return lastErr
}

// dial opens an authenticated NATS connection using the creds blob at credsPath.
// Reconnects are disabled so authorization failures surface immediately.
func dial(t *testing.T, url, credsPath string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url,
		nats.UserCredentials(credsPath),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(0),
	)
	if err != nil {
		t.Fatalf("nats.Connect(%s): %v", url, err)
	}
	return nc
}

// dialWithErrCh opens a NATS connection and routes async errors (the channel
// NATS uses to surface permission violations) to errCh. Buffered so a slow
// receiver doesn't deadlock the NATS client.
func dialWithErrCh(t *testing.T, url, credsPath string) (*nats.Conn, chan error) {
	t.Helper()
	errCh := make(chan error, 8)
	nc, err := nats.Connect(url,
		nats.UserCredentials(credsPath),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(0),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errCh <- e:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("nats.Connect(%s): %v", url, err)
	}
	return nc, errCh
}

// expectPermissionViolation waits for a permission-violation async error on errCh.
// Times out via the deadline so a missing rejection is a clear test failure.
func expectPermissionViolation(t *testing.T, errCh chan error, within time.Duration) {
	t.Helper()
	select {
	case got := <-errCh:
		if !strings.Contains(strings.ToLower(got.Error()), "permission") {
			t.Fatalf("expected a permissions violation error, got: %v", got)
		}
	case <-time.After(within):
		t.Fatal("expected a permissions violation error within deadline; none observed")
	}
}

// expectNoAsyncError fails if any async error arrives within the window. Used
// after publishes that the scope is supposed to allow.
func expectNoAsyncError(t *testing.T, errCh chan error, within time.Duration) {
	t.Helper()
	select {
	case got := <-errCh:
		t.Fatalf("unexpected async error: %v", got)
	case <-time.After(within):
	}
}

// extractNKeySeed pulls the seed line out of a NATS .creds blob for round-trip
// comparison. Returns the seed (e.g. "SU...") with no surrounding whitespace.
func extractNKeySeed(t *testing.T, creds string) string {
	t.Helper()
	const start = "-----BEGIN USER NKEY SEED-----"
	const end = "------END USER NKEY SEED------"
	i := strings.Index(creds, start)
	j := strings.Index(creds, end)
	if i < 0 || j < 0 || j <= i {
		t.Fatalf("creds missing NKey seed block:\n%s", creds)
	}
	return strings.TrimSpace(creds[i+len(start) : j])
}

// mapKeys returns the keys of a generic map. Used by export-encoding tests to
// surface a useful failure message when an expected key is missing.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
