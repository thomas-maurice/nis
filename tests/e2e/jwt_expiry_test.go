//go:build e2e

// jwt_expiry_test.go — end-to-end tests for the JWT lifecycle feature (P2):
// revocation, expiry policy, sweeper alerts, auto-renew, and reinstatement.
// No NATS server is needed for most scenarios; only TestJWTLifecycle_RevokeUser_NATSRejects
// boots a live container.
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	natsjwt "github.com/nats-io/jwt/v2"
	natsgo "github.com/nats-io/nats.go"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// ---------------------------------------------------------------------------
// helpers local to this file
// ---------------------------------------------------------------------------

// setJWTPolicy is a thin wrapper so test bodies stay readable.
func (h *harness) setJWTPolicy(t *testing.T, operatorID string, req *nisv1.SetJWTPolicyRequest) {
	t.Helper()
	req.Id = operatorID
	if _, err := h.operatorCli.SetJWTPolicy(context.Background(), connect.NewRequest(req)); err != nil {
		t.Fatalf("SetJWTPolicy: %v", err)
	}
}

// runSweep calls RunJWTExpirySweep and returns the response. Fatals on error.
func (h *harness) runSweep(t *testing.T) *nisv1.RunJWTExpirySweepResponse {
	t.Helper()
	resp, err := h.operatorCli.RunJWTExpirySweep(context.Background(), connect.NewRequest(&nisv1.RunJWTExpirySweepRequest{}))
	if err != nil {
		t.Fatalf("RunJWTExpirySweep: %v", err)
	}
	return resp.Msg
}

// listEventsOfType returns all events of the given type for the given resource ID.
func (h *harness) listEventsOfType(t *testing.T, eventType, resourceID string) []*nisv1.Event {
	t.Helper()
	resp, err := h.eventCli.ListEvents(context.Background(), connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			Types:      []string{eventType},
			ResourceId: resourceID,
			Limit:      100,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents(type=%s, resource=%s): %v", eventType, resourceID, err)
	}
	return resp.Msg.Events
}

// getUser is a convenience wrapper.
func (h *harness) getUser(t *testing.T, userID string) *nisv1.User {
	t.Helper()
	resp, err := h.userCli.GetUser(context.Background(), connect.NewRequest(&nisv1.GetUserRequest{Id: userID}))
	if err != nil {
		t.Fatalf("GetUser(%s): %v", userID, err)
	}
	return resp.Msg.User
}

// revokeUser calls RevokeUser and returns the updated user.
func (h *harness) revokeUser(t *testing.T, userID, reason string) *nisv1.User {
	t.Helper()
	resp, err := h.userCli.RevokeUser(context.Background(), connect.NewRequest(&nisv1.RevokeUserRequest{
		Id:     userID,
		Reason: reason,
	}))
	if err != nil {
		t.Fatalf("RevokeUser(%s): %v", userID, err)
	}
	return resp.Msg.User
}

// ---------------------------------------------------------------------------
// Test 1: no expiry by default
// ---------------------------------------------------------------------------

// TestJWTLifecycle_UserHasNoExpiryByDefault verifies that with the default
// operator policy (all-zero) a newly created user carries no exp claim.
// JwtIssuedAt must be non-nil (proves the JWT was minted), JwtExpiresAt must
// be nil (proves no TTL was applied).
func TestJWTLifecycle_UserHasNoExpiryByDefault(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "no-expiry-op")
	accID := h.createAccount(t, opID, "no-expiry-acc")
	userID := h.createUser(t, accID, "no-expiry-user")

	user, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: userID}))
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	u := user.Msg.User

	if u.JwtIssuedAt == nil {
		t.Error("JwtIssuedAt must be non-nil for a freshly minted user")
	}
	if u.JwtExpiresAt != nil {
		t.Errorf("JwtExpiresAt must be nil with default (no-expiry) policy, got %v", u.JwtExpiresAt.AsTime())
	}
}

// ---------------------------------------------------------------------------
// Test 2: user JWT carries exp after policy change
// ---------------------------------------------------------------------------

// TestJWTLifecycle_UserJWTHasExpAfterPolicyChange sets user_jwt_ttl=86400 on
// an operator, creates a NEW user, and verifies:
//   - user.JwtExpiresAt is approximately now+24h (within a 60s window)
//   - the embedded JWT's exp claim matches JwtExpiresAt
func TestJWTLifecycle_UserJWTHasExpAfterPolicyChange(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "ttl-op")
	accID := h.createAccount(t, opID, "ttl-acc")

	ttl := int64(86400)
	h.setJWTPolicy(t, opID, &nisv1.SetJWTPolicyRequest{
		UserJwtTtlSeconds: &ttl,
	})

	// Create a NEW user AFTER the policy is set.
	before := time.Now()
	userID := h.createUser(t, accID, "ttl-user")
	after := time.Now()

	u, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: userID}))
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	user := u.Msg.User

	if user.JwtExpiresAt == nil {
		t.Fatal("JwtExpiresAt must be non-nil after setting user_jwt_ttl=86400")
	}

	exp := user.JwtExpiresAt.AsTime()
	wantLo := before.Add(86400 * time.Second)
	wantHi := after.Add(86400 * time.Second).Add(60 * time.Second)
	if exp.Before(wantLo) || exp.After(wantHi) {
		t.Errorf("JwtExpiresAt=%v not in expected range [%v, %v]", exp, wantLo, wantHi)
	}

	// Decode the embedded JWT and check the exp claim.
	claims, err := natsjwt.DecodeUserClaims(user.Jwt)
	if err != nil {
		t.Fatalf("DecodeUserClaims: %v", err)
	}
	if claims.Expires == 0 {
		t.Fatal("embedded JWT has no exp claim")
	}
	embeddedExp := time.Unix(claims.Expires, 0)
	if !embeddedExp.Equal(exp) && embeddedExp.Sub(exp).Abs() > 2*time.Second {
		t.Errorf("embedded JWT exp=%v does not match JwtExpiresAt=%v (delta > 2s)", embeddedExp, exp)
	}
}

// ---------------------------------------------------------------------------
// Test 3: revoked user is rejected by NATS
// ---------------------------------------------------------------------------

// TestJWTLifecycle_RevokeUser_NATSRejects verifies the full revocation loop:
// create user → connect to NATS → revoke → sync → reconnect is rejected.
// This test requires a live NATS container.
func TestJWTLifecycle_RevokeUser_NATSRejects(t *testing.T) {
	h := startStack(t)

	s := h.bootStandardStack(t, "revoke-nats")

	// Pre-revocation: the user can connect and the connection is healthy.
	ncBefore := dial(t, h.natsURL, s.credsPath)
	if err := ncBefore.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("pre-revocation flush: %v", err)
	}
	ncBefore.Close()

	// Revoke the user. The handler immediately pushes the updated account JWT
	// (with the user's pubkey in its Revocations map) to all clusters.
	h.revokeUser(t, s.userID, "e2e revocation test")

	// syncCluster ensures the resolver on the NATS side has the fresh account
	// JWT even if the push above failed transiently.
	h.syncCluster(t, s.clusterID)

	// Short pause to let NATS ingest the pushed JWT.
	time.Sleep(500 * time.Millisecond)

	// Reconnect attempt must fail: NATS should reject the revoked pubkey.
	ncAfter, err := natsgo.Connect(h.natsURL,
		natsgo.UserCredentials(s.credsPath),
		natsgo.Timeout(5*time.Second),
		natsgo.MaxReconnects(0),
	)
	if err == nil {
		ncAfter.Close()
		t.Fatal("expected post-revocation connection to be rejected; it succeeded")
	}
}

// ---------------------------------------------------------------------------
// Test 4: revocation row is pruned after user JWT exp, event fired
// ---------------------------------------------------------------------------

// TestJWTLifecycle_RevocationListPrunedAfterUserJWTExp sets a 2s TTL, creates
// and revokes a user, waits for the JWT to expire, then runs the sweeper and
// asserts a user.revocation_pruned event was emitted for the account.
func TestJWTLifecycle_RevocationListPrunedAfterUserJWTExp(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "prune-op")
	accID := h.createAccount(t, opID, "prune-acc")

	ttl := int64(2) // 2s — minimum safe value for the wall-clock wait below
	h.setJWTPolicy(t, opID, &nisv1.SetJWTPolicyRequest{
		UserJwtTtlSeconds: &ttl,
	})

	userID := h.createUser(t, accID, "prune-user")
	h.revokeUser(t, userID, "prune test")

	// The revocation row's jwt_exp is ~ now+2s. Wait 3s so it is past exp.
	// Wall-clock sleep is justified here because the TTL is set to 2s and there
	// is no mechanism to advance the server's clock from outside.
	time.Sleep(3 * time.Second)

	// Trigger the sweeper to run the prune phase.
	h.runSweep(t)

	// The revocation row should now be pruned. Assert via the events table:
	// a user.revocation_pruned event must exist for the account.
	evts, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			Types:     []string{"user.revocation_pruned"},
			AccountId: accID,
			Limit:     10,
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents(user.revocation_pruned): %v", err)
	}
	if len(evts.Msg.Events) == 0 {
		t.Fatal("expected at least one user.revocation_pruned event after sweep; got none")
	}

	// Verify the account JWT no longer carries the revoked pubkey in its
	// Revocations map (i.e. the prune actually cleared the entry from the JWT).
	accResp, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: accID}))
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	userResp, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: userID}))
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	acClaims, err := natsjwt.DecodeAccountClaims(accResp.Msg.Account.Jwt)
	if err != nil {
		t.Fatalf("DecodeAccountClaims: %v", err)
	}
	revokedPubKey := userResp.Msg.User.PublicKey
	if _, found := acClaims.Revocations[revokedPubKey]; found {
		t.Errorf("account JWT still carries revoked pubkey %s after pruning", revokedPubKey)
	}
}

// ---------------------------------------------------------------------------
// Test 5: expiring-soon alert is deduplicated
// ---------------------------------------------------------------------------

// TestJWTLifecycle_ExpiringSoonAlertOncePerIAT sets warn_window > TTL (so
// every existing user is always "expiring soon"), runs the sweeper twice, and
// asserts exactly one user.cred.expiring_soon event per user (dedup).
func TestJWTLifecycle_ExpiringSoonAlertOncePerIAT(t *testing.T) {
	h := startStack(t)

	opID := h.createOperator(t, "warn-op")
	accID := h.createAccount(t, opID, "warn-acc")

	// ttl=90d, warn_window=120d → users are always "expiring soon" from the
	// moment of creation.
	ttl := int64(90 * 24 * 3600)
	warnWindow := int64(120 * 24 * 3600)
	h.setJWTPolicy(t, opID, &nisv1.SetJWTPolicyRequest{
		UserJwtTtlSeconds:    &ttl,
		JwtWarnWindowSeconds: &warnWindow,
	})

	userID := h.createUser(t, accID, "warn-user")

	// First sweep: should emit exactly one expiring_soon event.
	h.runSweep(t)

	events1 := h.listEventsOfType(t, "user.cred.expiring_soon", userID)
	if len(events1) != 1 {
		t.Fatalf("after first sweep: expected 1 user.cred.expiring_soon event, got %d", len(events1))
	}

	// Second sweep: the dedup guard (LastExpiringWarnIAT == JWTIssuedAt) must
	// prevent a second emission.
	h.runSweep(t)

	events2 := h.listEventsOfType(t, "user.cred.expiring_soon", userID)
	if len(events2) != 1 {
		t.Fatalf("after second sweep: expected still 1 user.cred.expiring_soon event (dedup), got %d", len(events2))
	}
}

// ---------------------------------------------------------------------------
// Test 6: auto-renew mints a fresh JWT
// ---------------------------------------------------------------------------

// TestJWTLifecycle_AutoRenewMintsFreshJWT sets jwt_auto_renew=true with
// warn_window > TTL (everything is in warn territory), runs the sweeper, and
// asserts that the user's JWT changed and a user.cred.renewed event fired.
func TestJWTLifecycle_AutoRenewMintsFreshJWT(t *testing.T) {
	h := startStack(t)

	opID := h.createOperator(t, "autorenew-op")
	accID := h.createAccount(t, opID, "autorenew-acc")

	ttl := int64(90 * 24 * 3600)
	warnWindow := int64(120 * 24 * 3600)
	autoRenew := true
	h.setJWTPolicy(t, opID, &nisv1.SetJWTPolicyRequest{
		UserJwtTtlSeconds:    &ttl,
		JwtWarnWindowSeconds: &warnWindow,
		JwtAutoRenew:         &autoRenew,
	})

	userID := h.createUser(t, accID, "autorenew-user")

	// Capture the JWT before the sweep.
	before := h.getUser(t, userID).Jwt

	h.runSweep(t)

	// The JWT must have been replaced.
	after := h.getUser(t, userID).Jwt
	if after == before {
		t.Error("expected auto-renew to replace the user JWT, but it is unchanged")
	}
	if after == "" {
		t.Error("post-renew JWT must not be empty")
	}

	// A user.cred.renewed event must exist.
	renewedEvents := h.listEventsOfType(t, "user.cred.renewed", userID)
	if len(renewedEvents) == 0 {
		t.Error("expected at least one user.cred.renewed event after auto-renew sweep; got none")
	}
}

// ---------------------------------------------------------------------------
// Test 7: expired JWTs never auto-renew
// ---------------------------------------------------------------------------

// TestJWTLifecycle_ExpiredAlertsNeverAutoRenew sets ttl=2s and auto_renew=true,
// creates a user, waits for the JWT to expire, runs the sweeper, and asserts:
//   - user.Jwt is UNCHANGED (expired JWTs are not auto-renewed)
//   - exactly one user.cred.expired event fired
//   - zero user.cred.renewed events fired
func TestJWTLifecycle_ExpiredAlertsNeverAutoRenew(t *testing.T) {
	h := startStack(t)

	opID := h.createOperator(t, "norenew-op")
	accID := h.createAccount(t, opID, "norenew-acc")

	ttl := int64(2)
	autoRenew := true
	h.setJWTPolicy(t, opID, &nisv1.SetJWTPolicyRequest{
		UserJwtTtlSeconds: &ttl,
		JwtAutoRenew:      &autoRenew,
	})

	userID := h.createUser(t, accID, "norenew-user")
	originalJWT := h.getUser(t, userID).Jwt

	// Wait for the 2s TTL to lapse before running the sweep.
	time.Sleep(3 * time.Second)

	h.runSweep(t)

	// JWT must be unchanged (expired = do not auto-renew).
	refreshed := h.getUser(t, userID).Jwt
	if refreshed != originalJWT {
		t.Error("auto-renew must NOT run for already-expired JWTs, but JWT changed")
	}

	// Exactly one user.cred.expired event.
	expiredEvents := h.listEventsOfType(t, "user.cred.expired", userID)
	if len(expiredEvents) != 1 {
		t.Errorf("expected exactly 1 user.cred.expired event, got %d", len(expiredEvents))
	}

	// Zero user.cred.renewed events.
	renewedEvents := h.listEventsOfType(t, "user.cred.renewed", userID)
	if len(renewedEvents) != 0 {
		t.Errorf("expected 0 user.cred.renewed events for expired JWT (auto-renew disabled for expired), got %d", len(renewedEvents))
	}
}

// ---------------------------------------------------------------------------
// Test 8: RegenerateCredentials reinstates a revoked user
// ---------------------------------------------------------------------------

// TestJWTLifecycle_RegenerateCredsReinstatesRevokedUser revokes a user,
// confirms revoked_at is set, calls RegenerateUserCredentials, and verifies:
//   - revoked_at is cleared
//   - revocation_reason is empty
//   - JWT changed
//   - returned .creds blob contains the expected NATS markers
func TestJWTLifecycle_RegenerateCredsReinstatesRevokedUser(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "reinstate-op")
	accID := h.createAccount(t, opID, "reinstate-acc")
	userID := h.createUser(t, accID, "reinstate-user")

	oldJWT := h.getUser(t, userID).Jwt

	// Revoke.
	revoked := h.revokeUser(t, userID, "will reinstate")
	if revoked.RevokedAt == nil {
		t.Fatal("RevokeUser: RevokedAt must be non-nil in response")
	}

	// Reinstate via RegenerateUserCredentials.
	regenResp, err := h.userCli.RegenerateUserCredentials(ctx, connect.NewRequest(&nisv1.RegenerateUserCredentialsRequest{
		Id: userID,
	}))
	if err != nil {
		t.Fatalf("RegenerateUserCredentials: %v", err)
	}
	reinstated := regenResp.Msg.User
	creds := regenResp.Msg.Credentials

	if reinstated.RevokedAt != nil {
		t.Error("RevokedAt must be nil after RegenerateUserCredentials")
	}
	if reinstated.RevocationReason != "" {
		t.Errorf("RevocationReason must be empty after regeneration, got %q", reinstated.RevocationReason)
	}
	if reinstated.Jwt == oldJWT {
		t.Error("JWT must change after RegenerateUserCredentials")
	}
	if reinstated.Jwt == "" {
		t.Error("new JWT must not be empty")
	}

	// The .creds blob must contain the expected block markers.
	for _, marker := range []string{"BEGIN NATS USER JWT", "BEGIN USER NKEY SEED"} {
		if !strings.Contains(creds, marker) {
			t.Errorf("returned .creds missing %q marker", marker)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 9: operator-admin RBAC on RevokeUser
// ---------------------------------------------------------------------------

// TestJWTLifecycle_OperatorAdminCanRevokeOwnUsers_NotOthers creates two
// operators each with an account + user, creates an operator-admin API user
// scoped to op1, and verifies:
//   - revoking op2's user → CodePermissionDenied
//   - revoking op1's own user → success
func TestJWTLifecycle_OperatorAdminCanRevokeOwnUsers_NotOthers(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	op1ID := h.createOperator(t, "rbac-revoke-op1")
	acc1ID := h.createAccount(t, op1ID, "rbac-revoke-acc1")
	user1ID := h.createUser(t, acc1ID, "rbac-revoke-user1")

	op2ID := h.createOperator(t, "rbac-revoke-op2")
	acc2ID := h.createAccount(t, op2ID, "rbac-revoke-acc2")
	user2ID := h.createUser(t, acc2ID, "rbac-revoke-user2")

	// Create an operator-admin scoped to op1 only.
	const opAdminUser = "revoke-op1-admin"
	const opAdminPass = "revoke-op1-admin-password"
	adminSession := h.loginAs(t, adminUsername, adminPassword)
	if _, err := adminSession.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &op1ID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin, op1): %v", err)
	}

	opAdmin1 := h.loginAs(t, opAdminUser, opAdminPass)

	// Revoking op2's user must fail with PermissionDenied.
	_, err := opAdmin1.userCli.RevokeUser(ctx, connect.NewRequest(&nisv1.RevokeUserRequest{
		Id:     user2ID,
		Reason: "should be denied",
	}))
	if err == nil {
		t.Fatal("operator-admin should NOT be able to revoke a user in another operator; call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied revoking cross-operator user, got %v: %v", got, err)
	}

	// Revoking op1's own user must succeed.
	revokeResp, err := opAdmin1.userCli.RevokeUser(ctx, connect.NewRequest(&nisv1.RevokeUserRequest{
		Id:     user1ID,
		Reason: "op1-admin revocation",
	}))
	if err != nil {
		t.Fatalf("operator-admin should be able to revoke their own user, but got: %v", err)
	}
	if revokeResp.Msg.User.RevokedAt == nil {
		t.Error("RevokeUser succeeded but RevokedAt is nil")
	}
}

// ---------------------------------------------------------------------------
// Test 10: RunJWTExpirySweep requires admin
// ---------------------------------------------------------------------------

// TestJWTLifecycle_RunSweep_RequiresAdmin verifies that non-admin callers
// receive CodePermissionDenied for RunJWTExpirySweep, while admin succeeds
// with zero counters (empty DB from the test's perspective).
func TestJWTLifecycle_RunSweep_RequiresAdmin(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	opID := h.createOperator(t, "sweep-rbac-op")

	// Create a non-admin operator-admin user.
	const nonAdminUser = "sweep-op-admin"
	const nonAdminPass = "sweep-op-admin-password"
	adminSession := h.loginAs(t, adminUsername, adminPassword)
	if _, err := adminSession.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    nonAdminUser,
		Password:    nonAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	nonAdmin := h.loginAs(t, nonAdminUser, nonAdminPass)

	// Non-admin must be denied.
	_, err := nonAdmin.operatorCli.RunJWTExpirySweep(ctx, connect.NewRequest(&nisv1.RunJWTExpirySweepRequest{}))
	if err == nil {
		t.Fatal("non-admin should NOT be able to call RunJWTExpirySweep; call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied for non-admin RunJWTExpirySweep, got %v: %v", got, err)
	}

	// Admin must succeed, returning zero counters (no sweepable state).
	sweepResp := h.runSweep(t)
	if sweepResp.RevocationsPruned != 0 {
		t.Errorf("expected 0 revocations_pruned, got %d", sweepResp.RevocationsPruned)
	}
	if sweepResp.ExpiringSoonEmitted != 0 {
		t.Errorf("expected 0 expiring_soon_emitted, got %d", sweepResp.ExpiringSoonEmitted)
	}
	if sweepResp.ExpiredAlertsEmitted != 0 {
		t.Errorf("expected 0 expired_alerts_emitted, got %d", sweepResp.ExpiredAlertsEmitted)
	}
	if sweepResp.AutoRenewed != 0 {
		t.Errorf("expected 0 auto_renewed, got %d", sweepResp.AutoRenewed)
	}
}
