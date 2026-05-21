package commands

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage NATS accounts",
	Long:  `Create, list, update, and delete NATS accounts.`,
}

var accountCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new account",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountCreate,
}

var accountListCmd = &cobra.Command{
	Use:   "list OPERATOR_ID_OR_NAME",
	Short: "List accounts for an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountList,
}

var accountGetCmd = &cobra.Command{
	Use:   "get NAME",
	Short: "Get account details by name",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountGet,
}

var accountDeleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "Delete an account by name",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountDelete,
}

var accountJetStreamUsageCmd = &cobra.Command{
	Use:   "jetstream-usage NAME",
	Short: "Show live per-cluster JetStream usage for an account",
	Long: `Query every cluster attached to the account's operator over NATS and report
live JetStream usage (memory, storage, streams, consumers) per cluster.

Status column meanings:
  ok                  cluster responded; usage populated
  unreachable         dial failed, timed out, or cluster marked unhealthy
                      (use --include-unhealthy to retry)
  no-jetstream        cluster reachable but JetStream not enabled for account
  account-not-found   cluster has no record of the account (sync drift)
  error               other failure (see error column)`,
	Args: cobra.ExactArgs(1),
	RunE: runAccountJetStreamUsage,
}

var (
	accountOperatorID       string
	accountDescription      string
	accountMaxMemory        int64
	accountMaxStorage       int64
	accountMaxStreams       int32
	accountMaxConsumers     int32
	accountForce            bool
	accountJSUsageIncludeUH bool
)

func init() {
	rootCmd.AddCommand(accountCmd)

	accountCmd.AddCommand(accountCreateCmd)
	accountCmd.AddCommand(accountListCmd)
	accountCmd.AddCommand(accountGetCmd)
	accountCmd.AddCommand(accountDeleteCmd)
	accountCmd.AddCommand(accountJetStreamUsageCmd)

	// Create flags
	accountCreateCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	accountCreateCmd.Flags().StringVar(&accountDescription, "description", "", "account description")
	accountCreateCmd.Flags().Int64Var(&accountMaxMemory, "max-memory", 0, "max memory (bytes)")
	accountCreateCmd.Flags().Int64Var(&accountMaxStorage, "max-storage", 0, "max storage (bytes)")
	accountCreateCmd.Flags().Int32Var(&accountMaxStreams, "max-streams", 0, "max streams")
	accountCreateCmd.Flags().Int32Var(&accountMaxConsumers, "max-consumers", 0, "max consumers")
	_ = accountCreateCmd.MarkFlagRequired("operator")

	// Get flags
	accountGetCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	_ = accountGetCmd.MarkFlagRequired("operator")

	// Delete flags
	accountDeleteCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	accountDeleteCmd.Flags().BoolVarP(&accountForce, "force", "f", false, "skip confirmation prompt")
	_ = accountDeleteCmd.MarkFlagRequired("operator")

	// JetStream usage flags
	accountJetStreamUsageCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	accountJetStreamUsageCmd.Flags().BoolVar(&accountJSUsageIncludeUH, "include-unhealthy", false, "force a dial attempt on clusters marked unhealthy (slower)")
	_ = accountJetStreamUsageCmd.MarkFlagRequired("operator")
}

// runAccountJetStreamUsage fetches the account by name then queries every
// attached cluster for live JetStream usage. Per-cluster failures are surfaced
// as status rows in the table — the command exits 0 unless the account isn't
// found or the caller lacks permission, so it stays pipe-friendly.
func runAccountJetStreamUsage(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	operatorID, err := resolveOperatorID(accountOperatorID)
	if err != nil {
		return err
	}

	getResp, err := GetClient().Account.GetAccountByName(context.Background(), connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       name,
	}))
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	resp, err := GetClient().Account.GetAccountJetStreamUsage(context.Background(), connect.NewRequest(&nisv1.GetAccountJetStreamUsageRequest{
		AccountId:        getResp.Msg.Account.Id,
		IncludeUnhealthy: accountJSUsageIncludeUH,
	}))
	if err != nil {
		return fmt.Errorf("failed to query JetStream usage: %w", err)
	}

	if len(resp.Msg.Clusters) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No clusters attached to this operator")
		}
		return nil
	}

	limits := getResp.Msg.Account.JetstreamLimits
	if GetOutputFormat() == "table" {
		headers := []string{"CLUSTER", "STATUS", "MEMORY", "STORAGE", "STREAMS", "CONSUMERS", "ERROR"}
		rows := make([][]string, len(resp.Msg.Clusters))
		for i, c := range resp.Msg.Clusters {
			rows[i] = []string{
				c.ClusterName,
				client.JetStreamProbeBadge(c.Status),
				formatUsageVsLimit(c.Usage.GetMemoryUsed(), limitOrZero(limits, "memory"), c.Status),
				formatUsageVsLimit(c.Usage.GetStorageUsed(), limitOrZero(limits, "storage"), c.Status),
				formatCountVsLimit(int64(c.Usage.GetStreams()), limitOrZero(limits, "streams"), c.Status),
				formatCountVsLimit(int64(c.Usage.GetConsumers()), limitOrZero(limits, "consumers"), c.Status),
				truncErrMsg(c.ErrorMessage),
			}
		}
		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Clusters)
}

func limitOrZero(l *nisv1.JetStreamLimits, kind string) int64 {
	if l == nil {
		return 0
	}
	switch kind {
	case "memory":
		return l.MaxMemory
	case "storage":
		return l.MaxStorage
	case "streams":
		return int64(l.MaxStreams)
	case "consumers":
		return int64(l.MaxConsumers)
	}
	return 0
}

// hasUsageRow returns true when the status carries meaningful usage numbers
// (either real OK usage, or NOT_ACTIVATED which we want to render as zero).
func hasUsageRow(s nisv1.JetStreamProbeStatus) bool {
	return s == nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_OK ||
		s == nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_NOT_ACTIVATED
}

func formatUsageVsLimit(used uint64, max int64, status nisv1.JetStreamProbeStatus) string {
	if !hasUsageRow(status) {
		return "-"
	}
	if max <= 0 {
		return fmt.Sprintf("%s / -", humanBytes(used))
	}
	pct := float64(used) / float64(max) * 100
	return fmt.Sprintf("%s / %s (%.0f%%)", humanBytes(used), humanBytes(uint64(max)), pct)
}

func formatCountVsLimit(used, max int64, status nisv1.JetStreamProbeStatus) string {
	if !hasUsageRow(status) {
		return "-"
	}
	if max <= 0 {
		return fmt.Sprintf("%d / -", used)
	}
	pct := float64(used) / float64(max) * 100
	return fmt.Sprintf("%d / %d (%.0f%%)", used, max, pct)
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	suffix := "KMGTPE"[exp]
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), suffix)
}

func truncErrMsg(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func runAccountCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(accountOperatorID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&nisv1.CreateAccountRequest{
		OperatorId:  operatorID,
		Name:        name,
		Description: accountDescription,
	})

	// Add JetStream limits if provided
	if accountMaxMemory > 0 || accountMaxStorage > 0 || accountMaxStreams > 0 || accountMaxConsumers > 0 {
		req.Msg.JetstreamLimits = &nisv1.JetStreamLimits{
			MaxMemory:    accountMaxMemory,
			MaxStorage:   accountMaxStorage,
			MaxStreams:   accountMaxStreams,
			MaxConsumers: accountMaxConsumers,
		}
	}

	resp, err := GetClient().Account.CreateAccount(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Account.Id)
		return nil
	}

	printer.PrintSuccess("Account created successfully")
	return printer.PrintObject(resp.Msg.Account)
}

func runAccountList(cmd *cobra.Command, args []string) error {
	operatorIDOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(operatorIDOrName)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&nisv1.ListAccountsRequest{
		OperatorId: operatorID,
	})

	resp, err := GetClient().Account.ListAccounts(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list accounts: %w", err)
	}

	if len(resp.Msg.Accounts) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No accounts found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "OPERATOR", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Accounts))

		for i, acc := range resp.Msg.Accounts {
			createdAt := "-"
			if acc.CreatedAt != nil {
				createdAt = acc.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				client.AccountID(acc.Id),
				acc.Name,
				client.OperatorID(acc.OperatorId),
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Accounts)
}

func runAccountGet(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(accountOperatorID)
	if err != nil {
		return err
	}

	// Get account by name
	req := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       name,
	})

	resp, err := GetClient().Account.GetAccountByName(context.Background(), req)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Account.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.Account)
}

func runAccountDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(accountOperatorID)
	if err != nil {
		return err
	}

	// Get account by name to get ID
	getReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       name,
	})

	getResp, err := GetClient().Account.GetAccountByName(context.Background(), getReq)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	accountID := getResp.Msg.Account.Id

	// Confirm deletion unless --force is used
	if !accountForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("account", name) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	// Delete the account
	req := connect.NewRequest(&nisv1.DeleteAccountRequest{
		Id: accountID,
	})

	_, err = GetClient().Account.DeleteAccount(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Account '%s' deleted successfully", name)
	}

	return nil
}

// Helper function to resolve operator ID from ID or name
func resolveOperatorID(idOrName string) (string, error) {
	// Try as ID first
	req := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: idOrName,
	})

	resp, err := GetClient().Operator.GetOperator(context.Background(), req)
	if err == nil {
		return resp.Msg.Operator.Id, nil
	}

	// Try by name
	nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name: idOrName,
	})

	nameResp, err := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
	if err != nil {
		return "", fmt.Errorf("operator not found: %w", err)
	}

	return nameResp.Msg.Operator.Id, nil
}
