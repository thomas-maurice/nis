package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Inspect and control background jobs (A2 substrate)",
	Long: `Background jobs run on the JobRunner inside the NIS server.
Shipped handlers: events.retention_sweep, jobs.retention_sweep,
revocations.retention_sweep (24h recurring); webhook.deliver (per-delivery);
backup.sweep + backup.execute (P12); jwt.expiry_sweep (P2);
cluster.health.sweep + cluster.health_check (A15).

Admin-only — operator-admin and account-admin get PermissionDenied.`,
}

var jobListCmd = &cobra.Command{
	Use:   "list",
	Short: "List background jobs",
	RunE:  runJobList,
}

var jobGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get a single job",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobGet,
}

var jobRetryCmd = &cobra.Command{
	Use:   "retry ID",
	Short: "Re-enqueue a failed/dead-lettered/cancelled job for another attempt",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobRetry,
}

var jobCancelCmd = &cobra.Command{
	Use:   "cancel ID",
	Short: "Cancel a pending job (only succeeds on rows whose status is 'pending')",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobCancel,
}

var (
	jobTypes    []string
	jobStatuses []string
	jobSince    time.Duration
	jobUntil    time.Duration
	jobLimit    int
	jobCursor   string
)

func init() {
	rootCmd.AddCommand(jobCmd)
	jobCmd.AddCommand(jobListCmd)
	jobCmd.AddCommand(jobGetCmd)
	jobCmd.AddCommand(jobRetryCmd)
	jobCmd.AddCommand(jobCancelCmd)

	jobListCmd.Flags().StringSliceVar(&jobTypes, "type", nil, "filter by job type (repeatable)")
	jobListCmd.Flags().StringSliceVar(&jobStatuses, "status", nil,
		"filter by status: pending|running|succeeded|failed|dead_lettered|cancelled (repeatable)")
	jobListCmd.Flags().DurationVar(&jobSince, "since", 0, "show jobs created newer than this duration (e.g. 24h)")
	jobListCmd.Flags().DurationVar(&jobUntil, "until", 0, "show jobs created older than this duration")
	jobListCmd.Flags().IntVar(&jobLimit, "limit", 50, "maximum number of jobs to return (1..200)")
	jobListCmd.Flags().StringVar(&jobCursor, "cursor", "", "pagination cursor from a previous response")
}

// parseJobStatus maps a CLI string to the proto enum. Returns
// JOB_STATUS_UNSPECIFIED + an error on unknown values so the caller can
// surface a typo rather than silently widening the filter.
func parseJobStatus(s string) (nisv1.JobStatus, error) {
	switch strings.ToLower(s) {
	case "pending":
		return nisv1.JobStatus_JOB_STATUS_PENDING, nil
	case "running":
		return nisv1.JobStatus_JOB_STATUS_RUNNING, nil
	case "succeeded":
		return nisv1.JobStatus_JOB_STATUS_SUCCEEDED, nil
	case "failed":
		return nisv1.JobStatus_JOB_STATUS_FAILED, nil
	case "dead_lettered", "dead-lettered", "deadlettered":
		return nisv1.JobStatus_JOB_STATUS_DEAD_LETTERED, nil
	case "cancelled", "canceled":
		return nisv1.JobStatus_JOB_STATUS_CANCELLED, nil
	default:
		return nisv1.JobStatus_JOB_STATUS_UNSPECIFIED, fmt.Errorf("unknown status %q (valid: pending|running|succeeded|failed|dead_lettered|cancelled)", s)
	}
}

func runJobList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	filter := &nisv1.JobFilter{
		Types:  jobTypes,
		Limit:  int32(jobLimit),
		Cursor: jobCursor,
	}
	for _, s := range jobStatuses {
		pb, err := parseJobStatus(s)
		if err != nil {
			return err
		}
		filter.Statuses = append(filter.Statuses, pb)
	}
	if jobSince > 0 {
		t := time.Now().Add(-jobSince)
		filter.Since = timestamppb.New(t)
	}
	if jobUntil > 0 {
		t := time.Now().Add(-jobUntil)
		filter.Until = timestamppb.New(t)
	}

	resp, err := GetClient().Job.ListJobs(context.Background(), connect.NewRequest(&nisv1.ListJobsRequest{
		Filter: filter,
	}))
	if err != nil {
		return fmt.Errorf("failed to list jobs: %w", err)
	}

	if len(resp.Msg.Jobs) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No jobs found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "TYPE", "STATUS", "ATTEMPTS", "SCHEDULED", "LAST_ERROR"}
		rows := make([][]string, len(resp.Msg.Jobs))
		for i, j := range resp.Msg.Jobs {
			sched := "-"
			if j.ScheduledFor != nil {
				// Time discipline (SKILL §2): storage is UTC; display in the
				// viewer's local timezone via .Local() at the boundary.
				sched = j.ScheduledFor.AsTime().Local().Format(time.RFC3339)
			}
			attempts := fmt.Sprintf("%d/%d", j.Attempts, j.MaxAttempts)
			// Error messages are long free text — truncate to keep the row
			// readable. IDs and types render in full.
			lastErr := j.LastError
			if len(lastErr) > 60 {
				lastErr = lastErr[:57] + "..."
			}
			rows[i] = []string{client.JobID(j.Id), j.Type, client.JobStatusBadge(j.Status), attempts, sched, lastErr}
		}
		if err := printer.PrintTable(headers, rows); err != nil {
			return err
		}
		if resp.Msg.NextCursor != "" && GetOutputFormat() != "quiet" {
			printer.PrintMessage("Next cursor: %s", resp.Msg.NextCursor)
		}
		return nil
	}

	return printer.PrintList(resp.Msg.Jobs)
}

func runJobGet(cmd *cobra.Command, args []string) error {
	resp, err := GetClient().Job.GetJob(context.Background(), connect.NewRequest(&nisv1.GetJobRequest{Id: args[0]}))
	if err != nil {
		return fmt.Errorf("failed to get job: %w", err)
	}
	return renderJob(resp.Msg.Job)
}

func runJobRetry(cmd *cobra.Command, args []string) error {
	resp, err := GetClient().Job.RetryJob(context.Background(), connect.NewRequest(&nisv1.RetryJobRequest{Id: args[0]}))
	if err != nil {
		return fmt.Errorf("failed to retry job: %w", err)
	}
	return renderJob(resp.Msg.Job)
}

func runJobCancel(cmd *cobra.Command, args []string) error {
	resp, err := GetClient().Job.CancelJob(context.Background(), connect.NewRequest(&nisv1.CancelJobRequest{Id: args[0]}))
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}
	return renderJob(resp.Msg.Job)
}

func renderJob(j *nisv1.Job) error {
	if GetOutputFormat() == "quiet" {
		client.NewPrinter(GetOutputFormat()).PrintID(j.Id)
		return nil
	}
	if GetOutputFormat() != "table" {
		return client.NewPrinter(GetOutputFormat()).PrintObject(j)
	}

	formatT := func(t *timestamppb.Timestamp) string {
		if t == nil {
			return "-"
		}
		return t.AsTime().Local().Format(time.RFC3339)
	}

	pairs := []client.KVPair{
		{Key: "ID", Value: client.JobID(j.Id)},
		{Key: "Type", Value: j.Type},
		{Key: "Status", Value: client.JobStatusBadge(j.Status)},
		{Key: "Attempts", Value: fmt.Sprintf("%d / %d", j.Attempts, j.MaxAttempts)},
		{Key: "Dedup key", Value: emptyDash(j.DedupKey)},
		{Key: "Scheduled for", Value: formatT(j.ScheduledFor)},
		{Key: "Started at", Value: formatT(j.StartedAt)},
		{Key: "Completed at", Value: formatT(j.CompletedAt)},
		{Key: "Created at", Value: formatT(j.CreatedAt)},
		{Key: "Updated at", Value: formatT(j.UpdatedAt)},
		{Key: "Locked by", Value: emptyDash(j.LockedBy)},
		{Key: "Locked until", Value: formatT(j.LockedUntil)},
	}
	if j.LastError != "" {
		pairs = append(pairs, client.KVPair{Key: "Last error", Value: client.ErrorStyle().Render(j.LastError)})
	}
	if j.Payload != "" && j.Payload != "{}" {
		pairs = append(pairs, client.KVPair{Key: "Payload", Value: prettyJSON(j.Payload)})
	}

	fmt.Print(client.RenderKV(pairs))
	return nil
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
