//go:build e2e

// webhooks_test.go — end-to-end tests for the WebhookService.
// Tests stand up an httptest.NewServer to receive POST deliveries and verify
// HMAC signatures with pkg/webhooks.Verify.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	_ "github.com/mattn/go-sqlite3"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/pkg/webhooks"
)

// receivedPost captures one incoming webhook POST for assertion.
type receivedPost struct {
	headers   http.Header
	body      []byte
	eventType string
}

// waitFor polls fn every 100ms until it returns true or the timeout elapses.
// It fails the test with t.Fatalf on timeout.
func waitFor(t *testing.T, timeout time.Duration, name string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("waitFor %q: timed out after %s", name, timeout)
}

// newReceiver builds an httptest.Server that captures POSTs and validates HMAC.
// secret is read from the closure at request time, so it can be set after the
// server is started. Verified POSTs are sent to the returned channel.
// The server returns statusFn(callCount) — 0 means 200.
func newReceiver(t *testing.T, getSecret func() string, statusFn func(n int) int) (*httptest.Server, chan *receivedPost) {
	t.Helper()
	ch := make(chan *receivedPost, 32)
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		n := callCount
		secret := getSecret()

		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		// Reconstruct request with already-read body so Verify can re-read it.
		r.Body = io.NopCloser(newBytesReader(body))

		if _, err := webhooks.Verify(r, []byte(secret), 0); err != nil {
			t.Logf("HMAC verify failed: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		eventType, _ := payload["type"].(string)

		status := 200
		if statusFn != nil {
			if code := statusFn(n); code != 0 {
				status = code
			}
		}
		w.WriteHeader(status)

		if status == http.StatusOK {
			select {
			case ch <- &receivedPost{
				headers:   r.Header.Clone(),
				body:      body,
				eventType: eventType,
			}:
			default:
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// bytesReadCloser is a simple io.ReadCloser over a byte slice.
type bytesReadCloser struct {
	*bytesReaderWrapper
}

func (bytesReadCloser) Close() error { return nil }

type bytesReaderWrapper struct {
	data   []byte
	offset int
}

func newBytesReader(b []byte) io.ReadCloser {
	return bytesReadCloser{&bytesReaderWrapper{data: b}}
}

func (r *bytesReaderWrapper) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

// createWebhookSub is a convenience helper that creates a subscription and
// returns the subscription and the plaintext secret.
func createWebhookSub(t *testing.T, h *harness, operatorID, name, url string, eventTypes []string) (*nisv1.WebhookSubscription, string) {
	t.Helper()
	ctx := context.Background()
	resp, err := h.webhookCli.CreateWebhookSubscription(ctx, connect.NewRequest(&nisv1.CreateWebhookSubscriptionRequest{
		OperatorId: operatorID,
		Name:       name,
		Url:        url,
		EventTypes: eventTypes,
	}))
	if err != nil {
		t.Fatalf("CreateWebhookSubscription(%s): %v", name, err)
	}
	return resp.Msg.Subscription, resp.Msg.Secret
}

// TestWebhooks_SubscribeAndReceive_HMACValidated proves the full happy path:
// a subscription fires on matching events and the HMAC is valid.
func TestWebhooks_SubscribeAndReceive_HMACValidated(t *testing.T) {
	h := startStack(t)

	opID := h.createOperator(t, "wh-op")

	var secret string
	receiver, received := newReceiver(t, func() string { return secret }, nil)

	sub, s := createWebhookSub(t, h, opID, "acct-watcher", receiver.URL, []string{"account.created"})
	secret = s
	t.Logf("subscription id=%s", sub.Id)

	// Trigger an account.created event.
	h.createAccount(t, opID, "wh-account")

	var post *receivedPost
	waitFor(t, 10*time.Second, "webhook delivery", func() bool {
		select {
		case p := <-received:
			post = p
			return true
		default:
			return false
		}
	})

	// Assert body fields.
	var payload map[string]any
	if err := json.Unmarshal(post.body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := payload["type"]; got != "account.created" {
		t.Errorf("payload type: want account.created, got %v", got)
	}
	if got := payload["operator_id"]; got != opID {
		t.Errorf("payload operator_id: want %s, got %v", opID, got)
	}
	if got := payload["resource_type"]; got != "account" {
		t.Errorf("payload resource_type: want account, got %v", got)
	}

	// Assert required headers.
	for _, hdr := range []string{
		webhooks.HeaderEvent,
		webhooks.HeaderDelivery,
		webhooks.HeaderSubscription,
		webhooks.HeaderTimestamp,
		webhooks.HeaderSignature,
	} {
		if post.headers.Get(hdr) == "" {
			t.Errorf("missing required header: %s", hdr)
		}
	}
}

// TestWebhooks_FilterMismatchSkipped proves that a subscription with
// event_types=["user.created"] does NOT fire on account.created.
func TestWebhooks_FilterMismatchSkipped(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "filter-mismatch-op")

	var secret string
	receiver, received := newReceiver(t, func() string { return secret }, nil)

	_, s := createWebhookSub(t, h, opID, "user-watcher", receiver.URL, []string{"user.created"})
	secret = s

	// Emit account.created — should NOT match the user.created subscription.
	h.createAccount(t, opID, "filter-account")

	// Wait 3 seconds and assert nothing arrived.
	select {
	case p := <-received:
		t.Errorf("expected no delivery, but got event type=%s", p.eventType)
	case <-time.After(3 * time.Second):
		// good
	}
	_ = ctx
}

// TestWebhooks_WildcardFires proves that a subscription with event_types=["*"]
// fires on any event (admin only).
func TestWebhooks_WildcardFires(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "wildcard-op")

	var secret string
	receiver, received := newReceiver(t, func() string { return secret }, nil)

	_, s := createWebhookSub(t, h, opID, "wildcard-watcher", receiver.URL, []string{"*"})
	secret = s

	// account.created should fire the wildcard subscription.
	h.createAccount(t, opID, "wc-account")

	waitFor(t, 10*time.Second, "wildcard delivery", func() bool {
		select {
		case <-received:
			return true
		default:
			return false
		}
	})
	_ = ctx
}

// TestWebhooks_DisabledSubscriptionSkipped proves that a disabled subscription
// does not receive deliveries.
func TestWebhooks_DisabledSubscriptionSkipped(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "disabled-op")

	var secret string
	receiver, received := newReceiver(t, func() string { return secret }, nil)

	sub, s := createWebhookSub(t, h, opID, "disable-watcher", receiver.URL, []string{"account.created"})
	secret = s

	// Immediately disable it.
	disabled := false
	if _, err := h.webhookCli.UpdateWebhookSubscription(ctx, connect.NewRequest(&nisv1.UpdateWebhookSubscriptionRequest{
		Id:      sub.Id,
		Enabled: &disabled,
	})); err != nil {
		t.Fatalf("UpdateWebhookSubscription (disable): %v", err)
	}

	h.createAccount(t, opID, "disabled-account")

	select {
	case p := <-received:
		t.Errorf("disabled subscription should not receive delivery, got event=%s", p.eventType)
	case <-time.After(3 * time.Second):
		// good
	}
}

// TestWebhooks_RetryThenSuccess proves that a delivery that fails on the first
// attempt is retried and eventually marked succeeded.
func TestWebhooks_RetryThenSuccess(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "retry-op")

	var secret string
	var callCount int
	allReceived := make(chan *receivedPost, 32)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		n := callCount

		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(newBytesReader(body))

		if _, err := webhooks.Verify(r, []byte(secret), 0); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if n == 1 {
			// First call: return 500 to trigger retry.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		eventType, _ := payload["type"].(string)
		w.WriteHeader(http.StatusOK)
		select {
		case allReceived <- &receivedPost{headers: r.Header.Clone(), body: body, eventType: eventType}:
		default:
		}
	}))
	t.Cleanup(srv.Close)

	sub, s := createWebhookSub(t, h, opID, "retry-watcher", srv.URL, []string{"account.created"})
	secret = s

	h.createAccount(t, opID, "retry-account")

	// Wait for the successful (second) delivery, up to 15s.
	waitFor(t, 15*time.Second, "retry succeeded delivery", func() bool {
		select {
		case <-allReceived:
			return true
		default:
			return false
		}
	})

	if callCount < 2 {
		t.Errorf("expected at least 2 calls (1 fail + 1 success), got %d", callCount)
	}

	// List deliveries and verify status=succeeded.
	delResp, err := h.webhookCli.ListWebhookDeliveries(ctx, connect.NewRequest(&nisv1.ListWebhookDeliveriesRequest{
		SubscriptionId: sub.Id,
	}))
	if err != nil {
		t.Fatalf("ListWebhookDeliveries: %v", err)
	}

	var found bool
	for _, d := range delResp.Msg.Deliveries {
		if d.Status == "succeeded" {
			found = true
			if d.Attempt < 2 {
				t.Errorf("expected attempt >= 2, got %d", d.Attempt)
			}
			break
		}
	}
	if !found {
		t.Errorf("no succeeded delivery found; deliveries: %+v", delResp.Msg.Deliveries)
	}
}

// TestWebhooks_DeadLetterAtMaxAttempts proves that a delivery that always fails
// becomes dead_letter after max attempts.
func TestWebhooks_DeadLetterAtMaxAttempts(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "deadletter-op")

	var secret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(newBytesReader(body))
		// Validate HMAC so the worker doesn't abort for signature reasons.
		_, _ = webhooks.Verify(r, []byte(secret), 0)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	// Use max-attempts=3 to keep the test fast (harness already sets backoff-base=1s).
	// We override max-attempts at harness level: the harness starts the server
	// with --webhooks-max-attempts default (5). We accept up to 5 attempts here.
	sub, s := createWebhookSub(t, h, opID, "deadletter-watcher", srv.URL, []string{"account.created"})
	secret = s

	h.createAccount(t, opID, "deadletter-account")

	// Wait up to 60s for the delivery to become dead_letter.
	waitFor(t, 60*time.Second, "dead_letter delivery", func() bool {
		delResp, err := h.webhookCli.ListWebhookDeliveries(ctx, connect.NewRequest(&nisv1.ListWebhookDeliveriesRequest{
			SubscriptionId: sub.Id,
		}))
		if err != nil {
			return false
		}
		for _, d := range delResp.Msg.Deliveries {
			if d.Status == "dead_letter" {
				return true
			}
		}
		return false
	})

	// Confirm the dead_letter delivery attributes.
	delResp, err := h.webhookCli.ListWebhookDeliveries(ctx, connect.NewRequest(&nisv1.ListWebhookDeliveriesRequest{
		SubscriptionId: sub.Id,
	}))
	if err != nil {
		t.Fatalf("ListWebhookDeliveries: %v", err)
	}
	var dl *nisv1.WebhookDelivery
	for _, d := range delResp.Msg.Deliveries {
		if d.Status == "dead_letter" {
			dl = d
			break
		}
	}
	if dl == nil {
		t.Fatal("expected a dead_letter delivery")
	}
	if dl.LastResponseCode != http.StatusInternalServerError {
		t.Errorf("expected last_response_code=500, got %d", dl.LastResponseCode)
	}
}

// TestWebhooks_TestSubscriptionEnqueues proves that TestWebhookSubscription
// enqueues a webhook.test delivery that arrives at the receiver.
func TestWebhooks_TestSubscriptionEnqueues(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "testsub-op")

	var secret string
	receiver, received := newReceiver(t, func() string { return secret }, nil)

	sub, s := createWebhookSub(t, h, opID, "testsub-watcher", receiver.URL, []string{"webhook.test"})
	secret = s

	testResp, err := h.webhookCli.TestWebhookSubscription(ctx, connect.NewRequest(&nisv1.TestWebhookSubscriptionRequest{
		Id: sub.Id,
	}))
	if err != nil {
		t.Fatalf("TestWebhookSubscription: %v", err)
	}
	deliveryID := testResp.Msg.DeliveryId
	if deliveryID == "" {
		t.Fatal("TestWebhookSubscription must return a non-empty delivery_id")
	}

	var post *receivedPost
	waitFor(t, 10*time.Second, "webhook.test delivery", func() bool {
		select {
		case p := <-received:
			post = p
			return true
		default:
			return false
		}
	})

	// Assert body type is webhook.test.
	var payload map[string]any
	if err := json.Unmarshal(post.body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := payload["type"]; got != "webhook.test" {
		t.Errorf("expected type=webhook.test, got %v", got)
	}
	// Assert payload_json contains subscription_id.
	if payloadJSON, ok := payload["payload_json"].(string); ok {
		var inner map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &inner); err == nil {
			if got := inner["subscription_id"]; got != sub.Id {
				t.Errorf("payload_json.subscription_id: want %s, got %v", sub.Id, got)
			}
		}
	}

	// The delivery_id returned from TestWebhookSubscription must match a
	// row in ListWebhookDeliveries.
	delResp, err := h.webhookCli.ListWebhookDeliveries(ctx, connect.NewRequest(&nisv1.ListWebhookDeliveriesRequest{
		SubscriptionId: sub.Id,
	}))
	if err != nil {
		t.Fatalf("ListWebhookDeliveries: %v", err)
	}
	var found bool
	for _, d := range delResp.Msg.Deliveries {
		if d.Id == deliveryID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("delivery_id %s not found in ListWebhookDeliveries", deliveryID)
	}
}

// TestWebhooks_RBAC_OperatorAdminOwnOperatorOnly proves that an operator-admin
// can only create/list subscriptions for their own operator.
func TestWebhooks_RBAC_OperatorAdminOwnOperatorOnly(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opAID := h.createOperator(t, "rbac-wh-op-a")
	opBID := h.createOperator(t, "rbac-wh-op-b")

	const opAdminUser = "rbac-wh-op-admin"
	const opAdminPass = "rbac-wh-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin for A): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	// operator-admin creates sub for operator B — must be denied.
	_, err := opAdmin.webhookCli.CreateWebhookSubscription(ctx, connect.NewRequest(&nisv1.CreateWebhookSubscriptionRequest{
		OperatorId: opBID,
		Name:       "should-fail",
		Url:        "http://localhost:9999/noop",
		EventTypes: []string{"account.created"},
	}))
	if err == nil {
		t.Fatal("operator-admin should NOT be able to create sub for operator B, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied, got %v: %v", got, err)
	}

	// operator-admin creates sub for operator A — must succeed.
	subResp, err := opAdmin.webhookCli.CreateWebhookSubscription(ctx, connect.NewRequest(&nisv1.CreateWebhookSubscriptionRequest{
		OperatorId: opAID,
		Name:       "own-op-sub",
		Url:        "http://localhost:9999/noop",
		EventTypes: []string{"account.created"},
	}))
	if err != nil {
		t.Fatalf("operator-admin should be able to create sub for own operator A: %v", err)
	}
	if subResp.Msg.Subscription.OperatorId != opAID {
		t.Errorf("created sub has wrong operator_id: %s", subResp.Msg.Subscription.OperatorId)
	}

	// Admin creates a sub for operator B.
	if _, err := h.webhookCli.CreateWebhookSubscription(ctx, connect.NewRequest(&nisv1.CreateWebhookSubscriptionRequest{
		OperatorId: opBID,
		Name:       "admin-op-b-sub",
		Url:        "http://localhost:9999/noop",
		EventTypes: []string{"account.created"},
	})); err != nil {
		t.Fatalf("admin CreateWebhookSubscription for op B: %v", err)
	}

	// operator-admin lists subscriptions without operator filter — must see only A's.
	opAdminList, err := opAdmin.webhookCli.ListWebhookSubscriptions(ctx, connect.NewRequest(&nisv1.ListWebhookSubscriptionsRequest{}))
	if err != nil {
		t.Fatalf("operator-admin ListWebhookSubscriptions: %v", err)
	}
	for _, s := range opAdminList.Msg.Subscriptions {
		if s.OperatorId != opAID {
			t.Errorf("operator-admin sees sub for operator %s (not own operator %s)", s.OperatorId, opAID)
		}
	}

	// Admin lists subscriptions without operator filter — must see both A and B.
	adminList, err := h.webhookCli.ListWebhookSubscriptions(ctx, connect.NewRequest(&nisv1.ListWebhookSubscriptionsRequest{}))
	if err != nil {
		t.Fatalf("admin ListWebhookSubscriptions: %v", err)
	}
	opsSeen := make(map[string]bool)
	for _, s := range adminList.Msg.Subscriptions {
		opsSeen[s.OperatorId] = true
	}
	if !opsSeen[opAID] {
		t.Error("admin should see subscriptions for operator A")
	}
	if !opsSeen[opBID] {
		t.Error("admin should see subscriptions for operator B")
	}
}

// TestWebhooks_RBAC_OperatorAdmin_WildcardForbidden proves that operator-admins
// cannot create wildcard subscriptions (only admins can).
func TestWebhooks_RBAC_OperatorAdmin_WildcardForbidden(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "wc-rbac-op")

	const opAdminUser = "wc-rbac-op-admin"
	const opAdminPass = "wc-rbac-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opID,
	})); err != nil {
		t.Fatalf("CreateAPIUser: %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	_, err := opAdmin.webhookCli.CreateWebhookSubscription(ctx, connect.NewRequest(&nisv1.CreateWebhookSubscriptionRequest{
		OperatorId: opID,
		Name:       "wildcard-attempt",
		Url:        "http://localhost:9999/noop",
		EventTypes: []string{"*"},
	}))
	if err == nil {
		t.Fatal("operator-admin should NOT be able to create wildcard subscription, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied, got %v: %v", got, err)
	}
}

// TestWebhooks_SecretEncryptedAtRest verifies that the HMAC secret is not
// stored in plaintext in the SQLite database.
func TestWebhooks_SecretEncryptedAtRest(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	dbPath := filepath.Join(h.workDir, "nis.db")
	if dbPath == "" {
		t.Skip("dbPath not accessible from harness")
	}

	opID := h.createOperator(t, "secret-at-rest-op")
	sub, plaintext := createWebhookSub(t, h, opID, "secret-test", "http://localhost:9999/noop", []string{"account.created"})

	if plaintext == "" {
		t.Fatal("expected non-empty plaintext secret from CreateWebhookSubscription")
	}

	// Open the DB directly and read the encrypted_secret column.
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Skipf("cannot open SQLite for at-rest check: %v", err)
	}
	defer db.Close()

	var encryptedSecret string
	row := db.QueryRowContext(ctx, "SELECT encrypted_secret FROM webhook_subscriptions WHERE id = ?", sub.Id)
	if err := row.Scan(&encryptedSecret); err != nil {
		t.Fatalf("query encrypted_secret: %v", err)
	}

	if encryptedSecret == plaintext {
		t.Error("encrypted_secret must not equal plaintext secret — stored in cleartext!")
	}
	// Belt-and-suspenders: the plaintext should not appear as a substring.
	if len(plaintext) > 0 && contains(encryptedSecret, plaintext) {
		t.Errorf("encrypted_secret contains plaintext secret as substring (cleartext storage detected)")
	}
}

// contains is a simple string substring check to avoid importing strings in
// a way that triggers the linter about shadowing.
func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || len(s) > 0 && indexSubstr(s, sub) >= 0)
}

func indexSubstr(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
