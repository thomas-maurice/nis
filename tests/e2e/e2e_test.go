//go:build e2e

// Package e2e is the end-to-end test suite for NIS. It boots NIS from a fresh database,
// stands up a real NATS server in Docker authenticated against that operator's JWTs, then
// exercises the identity flow over Connect-RPC and asserts NATS-side permissions.
//
// Run with:    make test-e2e
// Or:          go test -tags=e2e -v ./tests/e2e/...
//
// Requirements:
//   - docker daemon running (for the NATS container).
//   - free TCP ports (test picks unused ports for NIS and NATS).
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

// TestE2E_FullLifecycle drives NIS from a fresh DB through operator/account/user creation,
// brings up a JWT-authenticated NATS, syncs the cluster, and asserts:
//
//	(1) a freshly minted user's .creds authenticates and can publish/subscribe;
//	(2) connecting without credentials is rejected by NATS;
//	(3) a second account is fully isolated from the first — a user in account B cannot
//	    receive messages published by a user in account A on the same subject.
//
// Each phase is a sub-test so failures localise.
//
// Known gap (discovered while building this suite, 2026-05-13): scoped signing keys
// created via CreateScopedSigningKey are not added to the account JWT's `signing_keys`
// list, so NATS rejects any user JWT signed by such a key as "Authorization Violation."
// AccountService.CreateAccount in particular generates the account JWT before its own
// default scoped key exists, and there is no flow that re-signs the account JWT when
// a scoped key is added. Filed as part of the C9/PermissionService work; until fixed,
// the third sub-test deliberately exercises account-level isolation rather than
// scoped-key permission enforcement.
func TestE2E_FullLifecycle(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.teardown)

	h.start(t)

	ctx := context.Background()

	// --- Operator ---------------------------------------------------------------
	operatorResp, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:        "e2e-operator",
		Description: "operator created by the e2e suite",
	}))
	if err != nil {
		t.Fatalf("CreateOperator: %v", err)
	}
	operatorID := operatorResp.Msg.Operator.Id

	// --- Generate NATS include config and boot NATS with JWT auth ---------------
	includeResp, err := h.operatorCli.GenerateInclude(ctx, connect.NewRequest(&nisv1.GenerateIncludeRequest{
		Id: operatorID,
	}))
	if err != nil {
		t.Fatalf("GenerateInclude: %v", err)
	}
	natsConfPath := filepath.Join(h.workDir, "nats-server.conf")
	if err := os.WriteFile(natsConfPath, []byte(includeResp.Msg.Config), 0o644); err != nil {
		t.Fatalf("write nats config: %v", err)
	}

	h.startNATS(t, natsConfPath)

	// --- Cluster ----------------------------------------------------------------
	clusterResp, err := h.clusterCli.CreateCluster(ctx, connect.NewRequest(&nisv1.CreateClusterRequest{
		OperatorId: operatorID,
		Name:       "e2e-cluster",
		ServerUrls: []string{h.natsURL},
	}))
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	clusterID := clusterResp.Msg.Cluster.Id

	// --- Account + default user --------------------------------------------------
	accountResp, err := h.accountCli.CreateAccount(ctx, connect.NewRequest(&nisv1.CreateAccountRequest{
		OperatorId: operatorID,
		Name:       "e2e-account",
	}))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	accountID := accountResp.Msg.Account.Id

	userResp, err := h.userCli.CreateUser(ctx, connect.NewRequest(&nisv1.CreateUserRequest{
		AccountId: accountID,
		Name:      "default-user",
	}))
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defaultUserID := userResp.Msg.User.Id

	// --- Sync cluster (pushes account JWTs to resolver) -------------------------
	if _, err := h.clusterCli.SyncCluster(ctx, connect.NewRequest(&nisv1.SyncClusterRequest{
		Id: clusterID,
	})); err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}

	defaultCredsPath := h.fetchUserCreds(t, defaultUserID, "default-user")

	t.Run("AuthorizedConnection_DefaultUser", func(t *testing.T) {
		nc := dial(t, h.natsURL, defaultCredsPath)
		defer nc.Close()
		if err := nc.FlushTimeout(2 * time.Second); err != nil {
			t.Fatalf("flush: %v", err)
		}
		sub, err := nc.SubscribeSync("e2e.ping")
		if err != nil {
			t.Fatalf("SubscribeSync: %v", err)
		}
		if err := nc.Publish("e2e.ping", []byte("hello")); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		msg, err := sub.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatalf("NextMsg: %v", err)
		}
		if string(msg.Data) != "hello" {
			t.Fatalf("unexpected payload: %q", msg.Data)
		}
	})

	t.Run("UnauthorizedConnection_NoCredsRejected", func(t *testing.T) {
		nc, err := nats.Connect(h.natsURL,
			nats.Timeout(3*time.Second),
			nats.MaxReconnects(0),
		)
		if err == nil {
			nc.Close()
			t.Fatal("expected NATS to refuse unauthenticated connection")
		}
	})

	t.Run("AccountIsolation_CrossAccountSubjectsDoNotLeak", func(t *testing.T) {
		// Second account in the same operator with its own user.
		accountBResp, err := h.accountCli.CreateAccount(ctx, connect.NewRequest(&nisv1.CreateAccountRequest{
			OperatorId: operatorID,
			Name:       "e2e-account-b",
		}))
		if err != nil {
			t.Fatalf("CreateAccount (B): %v", err)
		}
		accountBID := accountBResp.Msg.Account.Id

		userBResp, err := h.userCli.CreateUser(ctx, connect.NewRequest(&nisv1.CreateUserRequest{
			AccountId: accountBID,
			Name:      "user-b",
		}))
		if err != nil {
			t.Fatalf("CreateUser (B): %v", err)
		}
		userBID := userBResp.Msg.User.Id

		// Re-sync so the new account JWT is in the resolver.
		if _, err := h.clusterCli.SyncCluster(ctx, connect.NewRequest(&nisv1.SyncClusterRequest{Id: clusterID})); err != nil {
			t.Fatalf("SyncCluster (B): %v", err)
		}

		credsB := h.fetchUserCreds(t, userBID, "user-b")

		// Subscriber on account B.
		ncB := dial(t, h.natsURL, credsB)
		defer ncB.Close()
		subB, err := ncB.SubscribeSync("crossaccount.probe")
		if err != nil {
			t.Fatalf("SubscribeSync on B: %v", err)
		}
		if err := ncB.FlushTimeout(2 * time.Second); err != nil {
			t.Fatalf("flush B: %v", err)
		}

		// Publisher on account A (the default user from the parent test scope).
		ncA := dial(t, h.natsURL, defaultCredsPath)
		defer ncA.Close()
		if err := ncA.Publish("crossaccount.probe", []byte("should not cross")); err != nil {
			t.Fatalf("Publish from A: %v", err)
		}
		if err := ncA.FlushTimeout(2 * time.Second); err != nil {
			t.Fatalf("flush A: %v", err)
		}

		if msg, err := subB.NextMsg(750 * time.Millisecond); err == nil {
			t.Fatalf("account isolation broken: user in account B received %q from account A", msg.Data)
		}

		// And within account B, pub/sub still works normally.
		if err := ncB.Publish("crossaccount.probe", []byte("from-B")); err != nil {
			t.Fatalf("Publish within B: %v", err)
		}
		msg, err := subB.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatalf("within-account-B NextMsg: %v", err)
		}
		if string(msg.Data) != "from-B" {
			t.Fatalf("unexpected payload within account B: %q", msg.Data)
		}
	})
}

// ----------------------------------------------------------------------------
// Harness
// ----------------------------------------------------------------------------

type harness struct {
	t       *testing.T
	workDir string
	repoDir string
	nisBin  string

	nisPort      int
	natsPort     int
	natsMgmtPort int

	serverURL string
	natsURL   string

	nisProcess    *exec.Cmd
	nisLogPath    string
	natsContainer string

	httpClient *http.Client
	authToken  string

	operatorCli nisv1connect.OperatorServiceClient
	accountCli  nisv1connect.AccountServiceClient
	userCli     nisv1connect.UserServiceClient
	clusterCli  nisv1connect.ClusterServiceClient
	keyCli      nisv1connect.ScopedSigningKeyServiceClient
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
	)
	h.nisProcess.Dir = h.workDir
	h.nisProcess.Env = append(os.Environ(),
		"AUTH_JWT_SECRET="+jwtSecret,
		"ENCRYPTION_KEY="+encryptionKey,
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
		"ENCRYPTION_KEY="+encryptionKey,
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
}

func (h *harness) startNATS(t *testing.T, confPath string) {
	t.Helper()
	args := []string{
		"run", "-d",
		"--name", h.natsContainer,
		"-p", fmt.Sprintf("127.0.0.1:%d:4222", h.natsPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:8222", h.natsMgmtPort),
		"-v", fmt.Sprintf("%s:/nats-server.conf:ro", confPath),
		"nats:2.10-alpine",
		"-c", "/nats-server.conf",
		"-m", "8222",
	}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start nats container: %v\n%s", err, out)
	}

	mgmtURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", h.natsMgmtPort)
	if err := waitForHTTP(mgmtURL, natsReadyTimeout); err != nil {
		dump, _ := exec.Command("docker", "logs", h.natsContainer).CombinedOutput()
		t.Fatalf("nats did not become healthy: %v\nnats logs:\n%s", err, dump)
	}
}

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

func (h *harness) teardown() {
	if h.nisProcess != nil && h.nisProcess.Process != nil {
		_ = h.nisProcess.Process.Kill()
		_, _ = h.nisProcess.Process.Wait()
	}
	if h.natsContainer != "" {
		_ = exec.Command("docker", "rm", "-f", h.natsContainer).Run()
	}
	if h.t.Failed() {
		if b, err := os.ReadFile(h.nisLogPath); err == nil {
			h.t.Logf("=== nis.log ===\n%s", b)
		}
	}
}

// ----------------------------------------------------------------------------
// Connect-RPC bearer-token interceptor
// ----------------------------------------------------------------------------

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

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

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
