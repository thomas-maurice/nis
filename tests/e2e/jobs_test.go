//go:build e2e

// jobs_test.go — A2 jobs substrate end-to-end coverage. Boots NIS (no
// NATS required — the in-v1 handlers don't touch NATS) and exercises the
// JobService admin surface plus the runtime behaviour of the
// jobs.retention_sweep handler.
package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_Jobs_AdminListShowsRetentionHandlers verifies the watchdog
// primed the recurring schedules at startup. After the JobRunner spins
// up, both retention handlers must have a pending row enqueued (their
// dedup key prevents duplicates from later ticks). This is the smoke test
// that proves serve.go actually wires the runner.
func TestE2E_Jobs_AdminListShowsRetentionHandlers(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// The watchdog primes on the first tick. Give the runner up to a few
	// poll intervals (10s in SQLite-dev mode) to fire its first tick.
	deadline := time.Now().Add(20 * time.Second)
	var seen map[string]bool
	for time.Now().Before(deadline) {
		resp, err := h.jobCli.ListJobs(ctx, connect.NewRequest(&nisv1.ListJobsRequest{
			Filter: &nisv1.JobFilter{Limit: 100},
		}))
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		seen = map[string]bool{}
		for _, j := range resp.Msg.Jobs {
			seen[j.Type] = true
		}
		if seen["events.retention_sweep"] && seen["jobs.retention_sweep"] {
			return // both handlers scheduled — substrate is live
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("watchdog did not schedule the retention handlers within deadline. seen=%v", seen)
}

// TestE2E_Jobs_OperatorAdminDenied: the JobService is admin-only. An
// operator-admin (the next privilege tier) must get PermissionDenied on
// every method. Regression guard against accidentally widening the
// resource scope when a future operator-scoped job type lands.
func TestE2E_Jobs_OperatorAdminDenied(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "jobs-rbac-op")

	const opAdminUser = "jobs-op-admin"
	const opAdminPass = "jobs-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	// ListJobs
	_, err := opAdmin.jobCli.ListJobs(ctx, connect.NewRequest(&nisv1.ListJobsRequest{Filter: &nisv1.JobFilter{Limit: 10}}))
	requireConnectCode(t, err, connect.CodePermissionDenied, "ListJobs(op-admin)")

	// GetJob — use a syntactically valid UUID so the auth check fires
	// before any "id parse failed" branch.
	_, err = opAdmin.jobCli.GetJob(ctx, connect.NewRequest(&nisv1.GetJobRequest{
		Id: "00000000-0000-0000-0000-000000000000",
	}))
	requireConnectCode(t, err, connect.CodePermissionDenied, "GetJob(op-admin)")

	// RetryJob
	_, err = opAdmin.jobCli.RetryJob(ctx, connect.NewRequest(&nisv1.RetryJobRequest{
		Id: "00000000-0000-0000-0000-000000000000",
	}))
	requireConnectCode(t, err, connect.CodePermissionDenied, "RetryJob(op-admin)")

	// CancelJob
	_, err = opAdmin.jobCli.CancelJob(ctx, connect.NewRequest(&nisv1.CancelJobRequest{
		Id: "00000000-0000-0000-0000-000000000000",
	}))
	requireConnectCode(t, err, connect.CodePermissionDenied, "CancelJob(op-admin)")
}

// TestE2E_Jobs_CancelPendingThenRetry exercises the admin write paths
// against a real running job row. The events.retention_sweep handler is a
// safe target — it has no side effects on a fresh empty DB (nothing to
// delete) so cancelling + retrying it doesn't perturb other tests.
func TestE2E_Jobs_CancelPendingThenRetry(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Wait for the watchdog to enqueue the retention handler.
	jobID := awaitPendingJob(t, h, "events.retention_sweep", 20*time.Second)

	// Cancel the pending row.
	cancelResp, err := h.jobCli.CancelJob(ctx, connect.NewRequest(&nisv1.CancelJobRequest{Id: jobID}))
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	if cancelResp.Msg.Job.Status != nisv1.JobStatus_JOB_STATUS_CANCELLED {
		t.Fatalf("after Cancel: status=%v, want CANCELLED", cancelResp.Msg.Job.Status)
	}

	// Cancelling again must fail with FailedPrecondition — the row is no
	// longer pending. This pins the state-machine guard in the repo.
	_, err = h.jobCli.CancelJob(ctx, connect.NewRequest(&nisv1.CancelJobRequest{Id: jobID}))
	requireConnectCode(t, err, connect.CodeFailedPrecondition, "Cancel-already-cancelled")

	// Retry resets the row to pending.
	retryResp, err := h.jobCli.RetryJob(ctx, connect.NewRequest(&nisv1.RetryJobRequest{Id: jobID}))
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if retryResp.Msg.Job.Status != nisv1.JobStatus_JOB_STATUS_PENDING {
		t.Fatalf("after Retry: status=%v, want PENDING", retryResp.Msg.Job.Status)
	}
	if retryResp.Msg.Job.Attempts != 0 {
		t.Fatalf("after Retry: attempts=%d, want 0 reset", retryResp.Msg.Job.Attempts)
	}

	// Retrying a row that's already pending must fail.
	_, err = h.jobCli.RetryJob(ctx, connect.NewRequest(&nisv1.RetryJobRequest{Id: jobID}))
	requireConnectCode(t, err, connect.CodeFailedPrecondition, "Retry-pending")
}

// TestE2E_Jobs_FilterByStatus exercises the filter pipe end-to-end:
// cancel one row, then list with statuses=[cancelled] and verify it's
// the only thing returned.
func TestE2E_Jobs_FilterByStatus(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	jobID := awaitPendingJob(t, h, "jobs.retention_sweep", 20*time.Second)
	if _, err := h.jobCli.CancelJob(ctx, connect.NewRequest(&nisv1.CancelJobRequest{Id: jobID})); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	resp, err := h.jobCli.ListJobs(ctx, connect.NewRequest(&nisv1.ListJobsRequest{
		Filter: &nisv1.JobFilter{
			Statuses: []nisv1.JobStatus{nisv1.JobStatus_JOB_STATUS_CANCELLED},
			Limit:    100,
		},
	}))
	if err != nil {
		t.Fatalf("ListJobs(cancelled): %v", err)
	}
	if len(resp.Msg.Jobs) == 0 {
		t.Fatalf("expected at least the cancelled row")
	}
	for _, j := range resp.Msg.Jobs {
		if j.Status != nisv1.JobStatus_JOB_STATUS_CANCELLED {
			t.Fatalf("filter leaked: got status=%v in cancelled-only result", j.Status)
		}
	}
}

// TestE2E_Jobs_GetUnknownIDReturnsNotFound: simple shape-of-error check.
// Catches regressions where the handler accidentally returns Internal or
// nil for a missing row.
func TestE2E_Jobs_GetUnknownIDReturnsNotFound(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	_, err := h.jobCli.GetJob(ctx, connect.NewRequest(&nisv1.GetJobRequest{
		Id: "11111111-1111-1111-1111-111111111111",
	}))
	requireConnectCode(t, err, connect.CodeNotFound, "Get(missing)")
}

// awaitPendingJob polls ListJobs until at least one row of the given type
// is in PENDING status, then returns its ID. Fails if the watchdog hasn't
// scheduled the type within `timeout`.
func awaitPendingJob(t *testing.T, h *harness, jobType string, timeout time.Duration) string {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := h.jobCli.ListJobs(ctx, connect.NewRequest(&nisv1.ListJobsRequest{
			Filter: &nisv1.JobFilter{
				Types:    []string{jobType},
				Statuses: []nisv1.JobStatus{nisv1.JobStatus_JOB_STATUS_PENDING},
				Limit:    1,
			},
		}))
		if err != nil {
			t.Fatalf("ListJobs(%s): %v", jobType, err)
		}
		if len(resp.Msg.Jobs) > 0 {
			return resp.Msg.Jobs[0].Id
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no pending job of type %q within %v", jobType, timeout)
	return ""
}

// requireConnectCode checks the error is a Connect-RPC error with the
// expected code. Mirrors the helper pattern in auth_rbac_test.go.
func requireConnectCode(t *testing.T, err error, want connect.Code, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error with code %v, got nil", label, want)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("%s: expected *connect.Error, got %T: %v", label, err, err)
	}
	if ce.Code() != want {
		t.Fatalf("%s: code=%v, want %v: %v", label, ce.Code(), want, err)
	}
}
