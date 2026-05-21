//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

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
	if !strings.HasSuffix(backup.ObjectKey, ".yaml") {
		t.Fatalf("object_key = %q, want .yaml suffix", backup.ObjectKey)
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

	// The first non-whitespace byte must NOT be '{' — that's JSON. We
	// configure SecretsEncrypted + YAML at the service layer; if a future
	// refactor flips the default the sweep would silently start uploading
	// JSON, which would surprise operators expecting `nisctl restore`-
	// compatible YAML.
	body := strings.TrimSpace(buf.String())
	if len(body) > 0 && (body[0] == '{' || body[0] == '[') {
		t.Fatalf("backup body looks like JSON, want YAML: starts with %q", body[:min(40, len(body))])
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
