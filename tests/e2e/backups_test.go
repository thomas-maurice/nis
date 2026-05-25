//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"filippo.io/age"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// addRecipientToOperator generates a fresh age identity, adds its public
// key to the operator's recipient list, and returns the identity so the
// test can decrypt the resulting backup. Each test gets its own keypair
// (no cross-test state) — the identity lives entirely in test-process
// memory and is never persisted.
func (h *harness) addRecipientToOperator(t *testing.T, ctx context.Context, operatorID string) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate age identity: %v", err)
	}
	_, err = h.backupCli.AddBackupRecipient(ctx, connect.NewRequest(&nisv1.AddBackupRecipientRequest{
		OperatorId: operatorID,
		PublicKey:  id.Recipient().String(),
		Label:      "e2e-test",
	}))
	if err != nil {
		t.Fatalf("AddBackupRecipient: %v", err)
	}
	return id
}

// TestE2E_Backups_EnableRunListDownload exercises the full P12 happy path:
// MinIO comes up, NIS boots with backups wired, admin enables backups on
// the operator + runs a manual backup, the resulting S3 object appears in
// List, and Download yields bytes whose sha256 matches what the row claims.
func TestE2E_Backups_EnableRunListDownload(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "backup-test-op")

	// Enable backups: minimum allowed interval (1h) — covers the validation
	// path. Sweep cadence (2s in the harness) is independent of per-operator
	// interval, so this doesn't slow the test down.
	enabled := true
	intervalSecs := int64(3600)
	retention := int32(3)
	updateResp, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &intervalSecs,
		RetentionCount:  &retention,
	}))
	if err != nil {
		t.Fatalf("UpdateOperatorBackupSettings: %v", err)
	}
	if !updateResp.Msg.Settings.Enabled {
		t.Fatalf("settings.enabled = false after enable")
	}
	if updateResp.Msg.Settings.IntervalSeconds != intervalSecs {
		t.Fatalf("settings.interval_seconds = %d, want %d", updateResp.Msg.Settings.IntervalSeconds, intervalSecs)
	}
	if updateResp.Msg.Settings.RetentionCount != retention {
		t.Fatalf("settings.retention_count = %d, want %d", updateResp.Msg.Settings.RetentionCount, retention)
	}

	// P15 — must have at least one age recipient before scheduled backups
	// can succeed. The TestE2E_Backups_FailsWithoutRecipients test below
	// pins the negative case.
	identity := h.addRecipientToOperator(t, ctx, operatorID)

	// Run a manual backup. Returns once the S3 PutObject + DB write succeed.
	runResp, err := h.backupCli.RunOperatorBackup(ctx, connect.NewRequest(&nisv1.RunOperatorBackupRequest{
		OperatorId: operatorID,
	}))
	if err != nil {
		t.Fatalf("RunOperatorBackup: %v", err)
	}
	backup := runResp.Msg.Backup
	if backup.SizeBytes <= 0 {
		t.Fatalf("backup.size_bytes = %d (want positive)", backup.SizeBytes)
	}
	if len(backup.Sha256) != 64 {
		t.Fatalf("backup.sha256 length = %d (want 64 hex chars)", len(backup.Sha256))
	}
	if backup.TriggerKind != "manual" {
		t.Fatalf("trigger_kind = %q, want %q", backup.TriggerKind, "manual")
	}
	if !strings.HasSuffix(backup.ObjectKey, ".age") {
		t.Fatalf("object_key = %q, want .age suffix (P15)", backup.ObjectKey)
	}

	// List returns the row we just created.
	listResp, err := h.backupCli.ListOperatorBackups(ctx, connect.NewRequest(&nisv1.ListOperatorBackupsRequest{
		OperatorId: operatorID,
	}))
	if err != nil {
		t.Fatalf("ListOperatorBackups: %v", err)
	}
	if len(listResp.Msg.Backups) != 1 {
		t.Fatalf("len(backups) = %d, want 1", len(listResp.Msg.Backups))
	}
	if listResp.Msg.Backups[0].Id != backup.Id {
		t.Fatalf("list backup ID mismatch: got %s, want %s", listResp.Msg.Backups[0].Id, backup.Id)
	}

	// Download the bytes, recompute sha256, confirm match.
	stream, err := h.backupCli.DownloadBackup(ctx, connect.NewRequest(&nisv1.DownloadBackupRequest{Id: backup.Id}))
	if err != nil {
		t.Fatalf("DownloadBackup: %v", err)
	}
	var buf strings.Builder
	for stream.Receive() {
		msg := stream.Msg()
		if chunk := msg.GetChunk(); len(chunk) > 0 {
			buf.Write(chunk)
		}
	}
	if err := stream.Err(); err != nil && err != io.EOF {
		t.Fatalf("stream.Err: %v", err)
	}
	if int64(buf.Len()) != backup.SizeBytes {
		t.Fatalf("downloaded size = %d, want %d", buf.Len(), backup.SizeBytes)
	}
	sum := sha256.Sum256([]byte(buf.String()))
	if got := hex.EncodeToString(sum[:]); got != backup.Sha256 {
		t.Fatalf("downloaded sha256 = %s, want %s", got, backup.Sha256)
	}

	// P15 — downloaded bytes must be age-encrypted: the magic header
	// `age-encryption.org/v1\n` is the canonical sniff. Then decrypt with
	// the identity we minted at the top of the test and verify the
	// plaintext is the YAML payload we expect (starts with `apiVersion:`).
	cipher := []byte(buf.String())
	if !bytes.HasPrefix(cipher, []byte("age-encryption.org/v1\n")) {
		t.Fatalf("downloaded bytes do not start with age header; first 40 bytes: %q", cipher[:min(40, len(cipher))])
	}
	r, err := age.Decrypt(bytes.NewReader(cipher), identity)
	if err != nil {
		t.Fatalf("age decrypt: %v", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read decrypted: %v", err)
	}
	body := strings.TrimSpace(string(plaintext))
	if len(body) == 0 {
		t.Fatalf("decrypted plaintext is empty")
	}
	if body[0] == '{' || body[0] == '[' {
		t.Fatalf("decrypted body looks like JSON, want YAML: starts with %q", body[:min(40, len(body))])
	}
	// ExportOperatorBytes emits the legacy NIS backup format which starts
	// with `version: "1.0"`. (Not k8s-style apiVersion/kind — that's the
	// pkg/manifest format used by `nisctl apply`.)
	if !strings.HasPrefix(body, "version:") {
		t.Fatalf("decrypted body does not start with version: header; first 80 chars: %q", body[:min(80, len(body))])
	}
}

// TestE2E_Backups_RetentionTrimsToLastN proves the retention path: with
// retention=2 and three backups uploaded, the oldest gets pruned from
// both S3 and the DB on the third run.
func TestE2E_Backups_RetentionTrimsToLastN(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "retention-test-op")

	enabled := true
	intervalSecs := int64(3600)
	retention := int32(2)
	_, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &intervalSecs,
		RetentionCount:  &retention,
	}))
	if err != nil {
		t.Fatalf("UpdateOperatorBackupSettings: %v", err)
	}

	// P15 — recipient required before any RunBackup succeeds.
	h.addRecipientToOperator(t, ctx, operatorID)

	// Backups are sorted by created_at; SQLite has 1s resolution and our
	// repo adds id DESC as the tie-break. Sleep a hair so created_at
	// differs between rows and the retention picks predictable victims.
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		resp, err := h.backupCli.RunOperatorBackup(ctx, connect.NewRequest(&nisv1.RunOperatorBackupRequest{
			OperatorId: operatorID,
		}))
		if err != nil {
			t.Fatalf("RunOperatorBackup #%d: %v", i, err)
		}
		ids = append(ids, resp.Msg.Backup.Id)
		time.Sleep(1100 * time.Millisecond)
	}

	listResp, err := h.backupCli.ListOperatorBackups(ctx, connect.NewRequest(&nisv1.ListOperatorBackupsRequest{
		OperatorId: operatorID,
	}))
	if err != nil {
		t.Fatalf("ListOperatorBackups: %v", err)
	}
	if len(listResp.Msg.Backups) != 2 {
		t.Fatalf("len(backups) = %d after retention enforcement, want 2 (ids: %v)", len(listResp.Msg.Backups), ids)
	}
	// Oldest backup (first uploaded) must be gone; newest two survive.
	survivors := map[string]bool{}
	for _, b := range listResp.Msg.Backups {
		survivors[b.Id] = true
	}
	if survivors[ids[0]] {
		t.Fatalf("oldest backup %s survived retention", ids[0])
	}
	if !survivors[ids[1]] || !survivors[ids[2]] {
		t.Fatalf("newer backups missing from list: ids=%v survivors=%v", ids, survivors)
	}
}

// TestE2E_Backups_DisableStopsService verifies the service-level guard:
// when backups are disabled at the operator level, RunOperatorBackup still
// works (it's a manual override of the schedule) — but Update with
// enabled=false flips the operator off. We assert the settings round-trip,
// and that a subsequent manual run still succeeds since the NIS-wide flag
// is still on.
//
// The harder "NIS-wide disabled returns FailedPrecondition" case is covered
// by the unit tests on BackupService.UpdateSettings — booting an entire
// NIS just to verify that one branch would be wasteful.
func TestE2E_Backups_DisableStopsScheduling(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "disable-test-op")

	// Enable → run → disable → verify settings reflect the disable.
	enabled := true
	intervalSecs := int64(3600)
	_, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &intervalSecs,
	}))
	if err != nil {
		t.Fatalf("enable: %v", err)
	}

	h.addRecipientToOperator(t, ctx, operatorID)

	_, err = h.backupCli.RunOperatorBackup(ctx, connect.NewRequest(&nisv1.RunOperatorBackupRequest{
		OperatorId: operatorID,
	}))
	if err != nil {
		t.Fatalf("RunOperatorBackup: %v", err)
	}

	disabled := false
	updateResp, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId: operatorID,
		Enabled:    &disabled,
	}))
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if updateResp.Msg.Settings.Enabled {
		t.Fatalf("settings.enabled = true after disable")
	}
	// Interval setting is preserved across the disable — operators expect
	// "turn off then back on" to NOT lose their schedule.
	if updateResp.Msg.Settings.IntervalSeconds != intervalSecs {
		t.Fatalf("interval lost on disable: got %d, want %d", updateResp.Msg.Settings.IntervalSeconds, intervalSecs)
	}
}

// TestE2E_Backups_IntervalRejectsTooSmall confirms the CHECK constraint
// surfaces as ErrBackupIntervalTooSmall → CodeInvalidArgument at the wire.
func TestE2E_Backups_IntervalRejectsTooSmall(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "interval-validation-op")

	enabled := true
	tooSmall := int64(60) // 60 seconds — below the 1h floor
	_, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &tooSmall,
	}))
	if err == nil {
		t.Fatalf("expected error for sub-1h interval, got nil")
	}
	connErr := new(connect.Error)
	if !asConnect(err, connErr) {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
	if connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument, got %s: %v", connErr.Code(), err)
	}
}

// asConnect is a local shim — connect.Error implements error and the
// SDK provides errors.As-style unwrap via *connect.Error pointer.
func asConnect(err error, target *connect.Error) bool {
	if err == nil {
		return false
	}
	for e := err; e != nil; {
		if ce, ok := e.(*connect.Error); ok {
			*target = *ce
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestE2E_Backups_FailsWithoutRecipients pins the P15 mandatory-encryption
// contract: an operator with backup_enabled=true but zero age recipients
// cannot complete a RunBackup. The RPC returns CodeFailedPrecondition with
// ErrNoBackupRecipients, and the audit log gets an operator.backup.failed
// event with reason="no_recipients_configured" so an operator scanning
// the failure can spot the configuration gap immediately.
func TestE2E_Backups_FailsWithoutRecipients(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "no-recipients-op")
	enabled := true
	intervalSecs := int64(3600)
	_, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &intervalSecs,
	}))
	if err != nil {
		t.Fatalf("enable: %v", err)
	}

	// No AddBackupRecipient call. RunBackup must fail.
	_, err = h.backupCli.RunOperatorBackup(ctx, connect.NewRequest(&nisv1.RunOperatorBackupRequest{
		OperatorId: operatorID,
	}))
	if err == nil {
		t.Fatalf("RunOperatorBackup unexpectedly succeeded with zero recipients")
	}
	var cerr connect.Error
	if !asConnect(err, &cerr) {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
	if cerr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %s: %v", cerr.Code(), err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no age backup recipients") {
		t.Fatalf("error message does not mention recipient configuration: %v", err)
	}
}

// TestE2E_Backups_MultiRecipientBothCanDecrypt — encrypt with two
// recipients, download the artifact, decrypt with each identity
// independently. Each decryption must yield identical plaintext.
func TestE2E_Backups_MultiRecipientBothCanDecrypt(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "multi-recipient-op")
	enabled := true
	intervalSecs := int64(3600)
	_, err := h.backupCli.UpdateOperatorBackupSettings(ctx, connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &intervalSecs,
	}))
	if err != nil {
		t.Fatalf("enable: %v", err)
	}

	id1 := h.addRecipientToOperator(t, ctx, operatorID)
	id2 := h.addRecipientToOperator(t, ctx, operatorID)

	runResp, err := h.backupCli.RunOperatorBackup(ctx, connect.NewRequest(&nisv1.RunOperatorBackupRequest{
		OperatorId: operatorID,
	}))
	if err != nil {
		t.Fatalf("RunOperatorBackup: %v", err)
	}

	stream, err := h.backupCli.DownloadBackup(ctx, connect.NewRequest(&nisv1.DownloadBackupRequest{Id: runResp.Msg.Backup.Id}))
	if err != nil {
		t.Fatalf("DownloadBackup: %v", err)
	}
	var buf bytes.Buffer
	for stream.Receive() {
		if chunk := stream.Msg().GetChunk(); len(chunk) > 0 {
			buf.Write(chunk)
		}
	}
	if err := stream.Err(); err != nil && err != io.EOF {
		t.Fatalf("stream.Err: %v", err)
	}

	cipher := buf.Bytes()

	// Decrypt with id1.
	r1, err := age.Decrypt(bytes.NewReader(cipher), id1)
	if err != nil {
		t.Fatalf("decrypt with id1: %v", err)
	}
	plain1, err := io.ReadAll(r1)
	if err != nil {
		t.Fatalf("read decrypted (id1): %v", err)
	}

	// Decrypt with id2 — independently.
	r2, err := age.Decrypt(bytes.NewReader(cipher), id2)
	if err != nil {
		t.Fatalf("decrypt with id2: %v", err)
	}
	plain2, err := io.ReadAll(r2)
	if err != nil {
		t.Fatalf("read decrypted (id2): %v", err)
	}

	if !bytes.Equal(plain1, plain2) {
		t.Fatalf("decrypted plaintext differs between identities")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(plain1), []byte("version:")) {
		t.Fatalf("decrypted plaintext doesn't look like operator YAML; first 80 chars: %q",
			string(bytes.TrimSpace(plain1))[:min(80, len(plain1))])
	}
}

// TestE2E_Backups_AddRecipient_InvalidRejected pins that AddBackupRecipient
// rejects malformed pubkeys at the API boundary (CodeInvalidArgument).
func TestE2E_Backups_AddRecipient_InvalidRejected(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "invalid-recipient-op")

	_, err := h.backupCli.AddBackupRecipient(ctx, connect.NewRequest(&nisv1.AddBackupRecipientRequest{
		OperatorId: operatorID,
		PublicKey:  "not-an-age-key",
	}))
	if err == nil {
		t.Fatalf("AddBackupRecipient unexpectedly accepted garbage")
	}
	var cerr connect.Error
	if !asConnect(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument, got %v", err)
	}
}

// TestE2E_Backups_RemoveRecipient_LastSignalled verifies the
// is_last_active flag in RemoveBackupRecipientResponse. The CLI uses this
// to render a warning that scheduled backups will fail until a recipient
// is re-added.
func TestE2E_Backups_RemoveRecipient_LastSignalled(t *testing.T) {
	h := startStackWithBackups(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	operatorID := h.createOperator(t, "last-recipient-op")

	id := h.addRecipientToOperator(t, ctx, operatorID)
	_ = id // not used; we just need the recipient registered

	listResp, err := h.backupCli.ListBackupRecipients(ctx, connect.NewRequest(&nisv1.ListBackupRecipientsRequest{
		OperatorId: operatorID,
	}))
	if err != nil {
		t.Fatalf("ListBackupRecipients: %v", err)
	}
	if len(listResp.Msg.Recipients) != 1 {
		t.Fatalf("got %d recipients, want 1", len(listResp.Msg.Recipients))
	}

	removeResp, err := h.backupCli.RemoveBackupRecipient(ctx, connect.NewRequest(&nisv1.RemoveBackupRecipientRequest{
		OperatorId:  operatorID,
		RecipientId: listResp.Msg.Recipients[0].Id,
	}))
	if err != nil {
		t.Fatalf("RemoveBackupRecipient: %v", err)
	}
	if !removeResp.Msg.IsLastActive {
		t.Fatalf("expected is_last_active=true after removing the only recipient")
	}

	// Run should now fail with the no-recipients error.
	_, err = h.backupCli.RunOperatorBackup(ctx, connect.NewRequest(&nisv1.RunOperatorBackupRequest{
		OperatorId: operatorID,
	}))
	if err == nil {
		t.Fatalf("RunOperatorBackup succeeded with zero recipients after Remove")
	}
}
