package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

// `nisctl operator backup ...` — manage scheduled S3 backups for an
// operator (P12). Distinct from the top-level `nisctl backup operator`
// which writes a one-shot YAML/JSON archive to local disk. These two
// surfaces coexist deliberately: the local-disk path is the existing
// disaster-recovery story; the scheduled path is hands-off ops.

var (
	opBackupInterval  time.Duration
	opBackupRetention int
	opBackupOutput    string
)

var operatorBackupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Manage scheduled S3 backups for an operator",
}

var operatorBackupEnableCmd = &cobra.Command{
	Use:   "enable OPERATOR_ID_OR_NAME",
	Short: "Enable scheduled backups for an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupEnable,
}

var operatorBackupDisableCmd = &cobra.Command{
	Use:   "disable OPERATOR_ID_OR_NAME",
	Short: "Disable scheduled backups for an operator (preserves settings)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupDisable,
}

var operatorBackupRunCmd = &cobra.Command{
	Use:   "run OPERATOR_ID_OR_NAME",
	Short: "Trigger a one-off backup right now (counts as trigger=manual in audit)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupRun,
}

var operatorBackupListCmd = &cobra.Command{
	Use:   "list OPERATOR_ID_OR_NAME",
	Short: "List uploaded backups for an operator (newest-first)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupList,
}

var operatorBackupShowCmd = &cobra.Command{
	Use:   "settings OPERATOR_ID_OR_NAME",
	Short: "Show backup configuration for an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupShow,
}

var operatorBackupDownloadCmd = &cobra.Command{
	Use:   "download BACKUP_ID",
	Short: "Download a backup's YAML payload from S3 to local disk",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupDownload,
}

var operatorBackupDeleteCmd = &cobra.Command{
	Use:   "delete BACKUP_ID",
	Short: "Remove one backup from S3 + the DB",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupDelete,
}

// P15 — per-operator age recipient management.

var (
	opBackupRecipientPubkey      string
	opBackupRecipientLabel       string
	opBackupRecipientIDOrPubkey  string
	opBackupRecipientConfirmYes  bool
)

var operatorBackupAddRecipientCmd = &cobra.Command{
	Use:   "add-recipient OPERATOR_ID_OR_NAME",
	Short: "Add an age recipient public key authorised to decrypt this operator's backups",
	Long: `Add an age recipient public key (X25519 or ssh-ed25519) authorised to
decrypt scheduled backups for this operator. The private half lives
wherever you choose — yubikey, encrypted disk, secret manager — and is
never seen by NIS. At least one recipient is required for scheduled
backups to succeed. Generate a local keypair with "nisctl backup keygen".`,
	Args: cobra.ExactArgs(1),
	RunE: runOperatorBackupAddRecipient,
}

var operatorBackupListRecipientsCmd = &cobra.Command{
	Use:   "list-recipients OPERATOR_ID_OR_NAME",
	Short: "List age recipients for an operator's scheduled backups",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupListRecipients,
}

var operatorBackupRemoveRecipientCmd = &cobra.Command{
	Use:   "remove-recipient OPERATOR_ID_OR_NAME",
	Short: "Remove an age recipient from an operator (by --recipient-id or --pubkey)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorBackupRemoveRecipient,
}

func init() {
	operatorBackupCmd.AddCommand(operatorBackupEnableCmd)
	operatorBackupCmd.AddCommand(operatorBackupDisableCmd)
	operatorBackupCmd.AddCommand(operatorBackupRunCmd)
	operatorBackupCmd.AddCommand(operatorBackupListCmd)
	operatorBackupCmd.AddCommand(operatorBackupShowCmd)
	operatorBackupCmd.AddCommand(operatorBackupDownloadCmd)
	operatorBackupCmd.AddCommand(operatorBackupDeleteCmd)
	operatorBackupCmd.AddCommand(operatorBackupAddRecipientCmd)
	operatorBackupCmd.AddCommand(operatorBackupListRecipientsCmd)
	operatorBackupCmd.AddCommand(operatorBackupRemoveRecipientCmd)
	operatorCmd.AddCommand(operatorBackupCmd)

	operatorBackupEnableCmd.Flags().DurationVar(&opBackupInterval, "interval", 24*time.Hour, "interval between scheduled backups (minimum 1h)")
	operatorBackupEnableCmd.Flags().IntVar(&opBackupRetention, "retain", 0, "keep at most N backups (0 = keep forever)")

	operatorBackupRunCmd.Flags().StringVarP(&opBackupOutput, "output", "o", "", "if set, write the resulting backup ID to this file (otherwise stdout)")

	operatorBackupDownloadCmd.Flags().StringVarP(&opBackupOutput, "output", "o", "", "output file path (default: <backup-id>.age)")

	operatorBackupAddRecipientCmd.Flags().StringVar(&opBackupRecipientPubkey, "pubkey", "", "age recipient public key (age1... or ssh-ed25519 ...) — required")
	operatorBackupAddRecipientCmd.Flags().StringVar(&opBackupRecipientLabel, "label", "", "optional human-readable tag")
	_ = operatorBackupAddRecipientCmd.MarkFlagRequired("pubkey")

	operatorBackupRemoveRecipientCmd.Flags().StringVar(&opBackupRecipientIDOrPubkey, "recipient-id", "", "recipient ID to remove (UUID; alternative: --pubkey)")
	operatorBackupRemoveRecipientCmd.Flags().StringVar(&opBackupRecipientPubkey, "pubkey", "", "recipient pubkey to remove (alternative: --recipient-id)")
	operatorBackupRemoveRecipientCmd.Flags().BoolVarP(&opBackupRecipientConfirmYes, "yes", "y", false, "skip the last-recipient confirmation prompt")
}

func runOperatorBackupEnable(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	enabled := true
	intervalSecs := int64(opBackupInterval.Seconds())
	retention := int32(opBackupRetention)
	req := &nisv1.UpdateOperatorBackupSettingsRequest{
		OperatorId:      operatorID,
		Enabled:         &enabled,
		IntervalSeconds: &intervalSecs,
		RetentionCount:  &retention,
	}
	resp, err := GetClient().Backup.UpdateOperatorBackupSettings(context.Background(), connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("enable backups: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	printer.PrintSuccess("Backups enabled for operator %s (interval=%s, retain=%d)", args[0], opBackupInterval, opBackupRetention)
	return printer.PrintObject(resp.Msg.Settings)
}

func runOperatorBackupDisable(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	enabled := false
	resp, err := GetClient().Backup.UpdateOperatorBackupSettings(context.Background(),
		connect.NewRequest(&nisv1.UpdateOperatorBackupSettingsRequest{
			OperatorId: operatorID,
			Enabled:    &enabled,
		}))
	if err != nil {
		return fmt.Errorf("disable backups: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	printer.PrintSuccess("Backups disabled for operator %s", args[0])
	return printer.PrintObject(resp.Msg.Settings)
}

func runOperatorBackupRun(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	resp, err := GetClient().Backup.RunOperatorBackup(context.Background(),
		connect.NewRequest(&nisv1.RunOperatorBackupRequest{OperatorId: operatorID}))
	if err != nil {
		return fmt.Errorf("run backup: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Backup.Id)
		return nil
	}
	printer.PrintSuccess("Backup uploaded: %s (%d bytes, sha256=%s)",
		resp.Msg.Backup.ObjectKey, resp.Msg.Backup.SizeBytes, resp.Msg.Backup.Sha256[:12])
	return printer.PrintObject(resp.Msg.Backup)
}

func runOperatorBackupList(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	resp, err := GetClient().Backup.ListOperatorBackups(context.Background(),
		connect.NewRequest(&nisv1.ListOperatorBackupsRequest{OperatorId: operatorID}))
	if err != nil {
		return fmt.Errorf("list backups: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	if len(resp.Msg.Backups) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No backups for operator %s", args[0])
		}
		return nil
	}
	if GetOutputFormat() == "table" {
		headers := []string{"ID", "CREATED", "SIZE", "TRIGGER", "SHA256", "OBJECT KEY"}
		rows := make([][]string, len(resp.Msg.Backups))
		for i, b := range resp.Msg.Backups {
			created := ""
			if b.CreatedAt != nil {
				created = b.CreatedAt.AsTime().Local().Format(time.RFC3339)
			}
			size := int64(0)
			if b.SizeBytes > 0 {
				size = b.SizeBytes
			}
			rows[i] = []string{
				client.BackupID(b.Id),
				created,
				humanBytes(uint64(size)),
				b.TriggerKind,
				b.Sha256,
				b.ObjectKey,
			}
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(resp.Msg.Backups)
}

func runOperatorBackupShow(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	resp, err := GetClient().Backup.GetOperatorBackupSettings(context.Background(),
		connect.NewRequest(&nisv1.GetOperatorBackupSettingsRequest{OperatorId: operatorID}))
	if err != nil {
		return fmt.Errorf("get backup settings: %w", err)
	}
	return client.NewPrinter(GetOutputFormat()).PrintObject(resp.Msg.Settings)
}

func runOperatorBackupDownload(cmd *cobra.Command, args []string) error {
	backupID := args[0]
	outPath := opBackupOutput
	if outPath == "" {
		// P15 — downloaded artifacts are age-encrypted; the .age suffix
		// matches age-tool conventions and signals to humans that the
		// file needs decrypting with `nisctl backup decrypt` or `age -d`
		// before it can be passed to `nisctl restore`.
		outPath = backupID + ".age"
	}
	stream, err := GetClient().Backup.DownloadBackup(context.Background(),
		connect.NewRequest(&nisv1.DownloadBackupRequest{Id: backupID}))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()
	var total int64
	for stream.Receive() {
		msg := stream.Msg()
		if chunk := msg.GetChunk(); len(chunk) > 0 {
			n, err := f.Write(chunk)
			if err != nil {
				return fmt.Errorf("write: %w", err)
			}
			total += int64(n)
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("stream: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	printer.PrintSuccess("Wrote %d bytes to %s", total, outPath)
	return nil
}

func runOperatorBackupDelete(cmd *cobra.Command, args []string) error {
	_, err := GetClient().Backup.DeleteBackup(context.Background(),
		connect.NewRequest(&nisv1.DeleteBackupRequest{Id: args[0]}))
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	client.NewPrinter(GetOutputFormat()).PrintSuccess("Backup %s deleted", args[0])
	return nil
}

// P15 — recipient subcommand runners.

func runOperatorBackupAddRecipient(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	resp, err := GetClient().Backup.AddBackupRecipient(context.Background(),
		connect.NewRequest(&nisv1.AddBackupRecipientRequest{
			OperatorId: operatorID,
			PublicKey:  opBackupRecipientPubkey,
			Label:      opBackupRecipientLabel,
		}))
	if err != nil {
		return fmt.Errorf("add recipient: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	printer.PrintSuccess("Recipient added: %s (label=%q)",
		resp.Msg.Recipient.Id, resp.Msg.Recipient.Label)
	return printer.PrintObject(resp.Msg.Recipient)
}

func runOperatorBackupListRecipients(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	resp, err := GetClient().Backup.ListBackupRecipients(context.Background(),
		connect.NewRequest(&nisv1.ListBackupRecipientsRequest{OperatorId: operatorID}))
	if err != nil {
		return fmt.Errorf("list recipients: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	if GetOutputFormat() == "table" {
		headers := []string{"ID", "LABEL", "PUBLIC KEY", "CREATED"}
		rows := make([][]string, 0, len(resp.Msg.Recipients))
		for _, r := range resp.Msg.Recipients {
			rows = append(rows, []string{
				r.Id,
				r.Label,
				r.PublicKey,
				r.CreatedAt.AsTime().Local().Format(time.RFC3339),
			})
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(resp.Msg.Recipients)
}

func runOperatorBackupRemoveRecipient(cmd *cobra.Command, args []string) error {
	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}
	if opBackupRecipientIDOrPubkey == "" && opBackupRecipientPubkey == "" {
		return fmt.Errorf("either --recipient-id or --pubkey is required")
	}
	if opBackupRecipientIDOrPubkey != "" && opBackupRecipientPubkey != "" {
		return fmt.Errorf("--recipient-id and --pubkey are mutually exclusive")
	}
	recipientID := opBackupRecipientIDOrPubkey
	if recipientID == "" {
		// Resolve by pubkey.
		listResp, err := GetClient().Backup.ListBackupRecipients(context.Background(),
			connect.NewRequest(&nisv1.ListBackupRecipientsRequest{OperatorId: operatorID}))
		if err != nil {
			return fmt.Errorf("resolve recipient: %w", err)
		}
		for _, r := range listResp.Msg.Recipients {
			if r.PublicKey == opBackupRecipientPubkey {
				recipientID = r.Id
				break
			}
		}
		if recipientID == "" {
			return fmt.Errorf("no recipient with that public key on this operator")
		}
	}
	resp, err := GetClient().Backup.RemoveBackupRecipient(context.Background(),
		connect.NewRequest(&nisv1.RemoveBackupRecipientRequest{
			OperatorId:  operatorID,
			RecipientId: recipientID,
		}))
	if err != nil {
		return fmt.Errorf("remove recipient: %w", err)
	}
	printer := client.NewPrinter(GetOutputFormat())
	printer.PrintSuccess("Recipient %s removed", recipientID)
	if resp.Msg.IsLastActive {
		fmt.Fprintln(os.Stderr, "WARNING: this operator now has ZERO age recipients; scheduled backups will fail on the next sweep until you add a recipient back.")
		if !opBackupRecipientConfirmYes {
			// The removal already happened; we just surface the warning loudly.
			// --yes does not suppress the warning, only the (currently-absent)
			// pre-removal prompt. Future v1.1 may add an interactive prompt
			// before the call; today the warning is post-hoc.
			fmt.Fprintln(os.Stderr, "Pass --yes to silence this warning in scripts that intentionally clear all recipients.")
		}
	}
	return nil
}

