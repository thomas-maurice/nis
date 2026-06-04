package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect"
	"filippo.io/age"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

// ageMagic is the literal header bytes every age artifact begins with. The
// CLI sniffs this before deciding to decrypt; see runRestoreOperator.
const ageMagic = "age-encryption.org/v1\n"

func isAgeEncrypted(data []byte) bool {
	return bytes.HasPrefix(data, []byte(ageMagic))
}

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Back up operators",
	Long:  `Create a lossless backup of an operator with all its accounts, users, scoped signing keys, and clusters. The resulting file can be fed to "nisctl restore" to recreate the operator on this or another NIS instance.`,
}

var backupOperatorCmd = &cobra.Command{
	Use:   "operator OPERATOR_ID_OR_NAME",
	Short: "Back up an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runBackupOperator,
}

var restoreCmd = &cobra.Command{
	Use:   "restore FILE",
	Short: "Restore an operator from a backup file (YAML or JSON)",
	Long: `Restore an operator from a previously-created backup file. Both YAML
and JSON encodings are accepted; the format is auto-detected from the file
contents (no --format flag needed).`,
	Args: cobra.ExactArgs(1),
	RunE: runRestoreOperator,
}

var importNSCCmd = &cobra.Command{
	Use:   "import-nsc ARCHIVE_FILE OPERATOR_NAME",
	Short: "Import an operator from an NSC archive (.zip, .tar.gz, .tar.bz2)",
	Long:  `Migrate an operator from an existing nsc store. This is a one-way migration tool — unlike "nisctl restore", the source format is an nsc archive, not a NIS backup.`,
	Args:  cobra.ExactArgs(2),
	RunE:  runImportNSC,
}

var (
	backupPlaintextSecrets bool
	backupOutput           string
	backupFormat           string
	restoreOverwrite       bool
	restoreIdentityFile    string
)

func init() {
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(importNSCCmd)

	backupCmd.AddCommand(backupOperatorCmd)

	backupOperatorCmd.Flags().BoolVar(&backupPlaintextSecrets, "plaintext-secrets", false,
		"DANGER: emit NKey seeds in plaintext (recovery from lost encryption key). "+
			"Default backups keep seeds encrypted with the server's current key.")
	backupOperatorCmd.Flags().StringVarP(&backupOutput, "output", "o", "", "output file (default: stdout)")
	backupOperatorCmd.Flags().StringVarP(&backupFormat, "format", "f", "yaml", "backup format: yaml or json")

	restoreCmd.Flags().BoolVar(&restoreOverwrite, "overwrite", false,
		"if an operator with the same ID already exists, atomically replace "+
			"its subtree (accounts, users, scoped keys) with the backup's "+
			"contents. Attached clusters are preserved. Without this flag, "+
			"restoring over an existing operator ID is refused.")
	restoreCmd.Flags().StringVarP(&restoreIdentityFile, "identity", "i", "",
		"age identity file (secret key) to decrypt the input. Required when "+
			"restoring a scheduled (P15) backup downloaded via "+
			"\"nisctl operator backup download\". Manual exports created via "+
			"\"nisctl backup operator\" are plaintext YAML/JSON and do not "+
			"need --identity.")
}

func runBackupOperator(cmd *cobra.Command, args []string) error {
	operatorIDOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Try to parse as UUID first, if that fails try by name
	var operatorID string
	getReq := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: operatorIDOrName,
	})

	getResp, err := GetClient().Operator.GetOperator(context.Background(), getReq)
	if err != nil {
		// Try by name
		nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name:           operatorIDOrName,
			OrganizationId: GetOrgID(),
		})

		nameResp, nameErr := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("operator not found: %w", nameErr)
		}

		operatorID = nameResp.Msg.Operator.Id
	} else {
		operatorID = getResp.Msg.Operator.Id
	}

	// Validate format flag early so a typo errors before the round-trip.
	format := strings.ToLower(strings.TrimSpace(backupFormat))
	switch format {
	case "yaml", "yml":
		format = "yaml"
	case "json":
		// keep as-is
	default:
		return fmt.Errorf("invalid --format %q: must be yaml or json", backupFormat)
	}

	if backupPlaintextSecrets {
		fmt.Fprintln(os.Stderr,
			"WARNING: --plaintext-secrets emits NKey seeds in plaintext. "+
				"The resulting file is a plaintext key vault; protect it like a "+
				".creds file. Anyone with read access can mint credentials for "+
				"every entity in the backup.")
	}

	req := connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId:       operatorID,
		Format:           format,
		PlaintextSecrets: backupPlaintextSecrets,
	})

	resp, err := GetClient().Export.ExportOperator(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to back up operator: %w", err)
	}

	if backupOutput != "" {
		if err := os.WriteFile(backupOutput, resp.Msg.Data, 0600); err != nil {
			return fmt.Errorf("failed to write backup file: %w", err)
		}
		if GetOutputFormat() != "quiet" {
			printer.PrintSuccess("Operator backed up to %s", backupOutput)
		}
	} else {
		fmt.Println(string(resp.Msg.Data))
	}

	return nil
}

func runRestoreOperator(cmd *cobra.Command, args []string) error {
	filename := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	// P15 — if the file is age-encrypted, decrypt it before passing to
	// ImportOperator. The age header starts with the literal bytes
	// "age-encryption.org/v1\n". Sniffing both directions lets us refuse
	// loudly when the user's --identity / file-shape choices don't match.
	isAge := isAgeEncrypted(data)
	switch {
	case isAge && restoreIdentityFile == "":
		return fmt.Errorf("file %s is age-encrypted but no --identity / -i was supplied", filename)
	case !isAge && restoreIdentityFile != "":
		return fmt.Errorf("file %s does not look age-encrypted; remove --identity / -i to restore the plaintext input", filename)
	case isAge && restoreIdentityFile != "":
		identities, err := loadAgeIdentities(restoreIdentityFile)
		if err != nil {
			return fmt.Errorf("load identity: %w", err)
		}
		r, err := age.Decrypt(bytes.NewReader(data), identities...)
		if err != nil {
			return fmt.Errorf("age decrypt: %w", err)
		}
		plaintext, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("read decrypted stream: %w", err)
		}
		data = plaintext
	}

	// Format (json vs yaml) is auto-detected by the server from the file contents.
	req := connect.NewRequest(&nisv1.ImportOperatorRequest{
		Data:      data,
		Overwrite: restoreOverwrite,
	})

	resp, err := GetClient().Export.ImportOperator(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to restore operator: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.OperatorId)
		return nil
	}

	printer.PrintSuccess("Operator restored successfully")
	fmt.Printf("Operator ID: %s\n", resp.Msg.OperatorId)

	return nil
}

func runImportNSC(cmd *cobra.Command, args []string) error {
	archiveFile := args[0]
	operatorName := args[1]
	printer := client.NewPrinter(GetOutputFormat())

	archiveData, err := os.ReadFile(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to read archive file: %w", err)
	}

	req := connect.NewRequest(&nisv1.ImportFromNSCRequest{
		Data:         archiveData,
		OperatorName: operatorName,
	})

	resp, err := GetClient().Export.ImportFromNSC(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to import from NSC: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.OperatorId)
		return nil
	}

	printer.PrintSuccess("Operator imported from NSC successfully")
	fmt.Printf("Operator ID: %s\n", resp.Msg.OperatorId)

	return nil
}
