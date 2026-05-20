package commands

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var signingKeyCmd = &cobra.Command{
	Use:     "signing-key",
	Aliases: []string{"scoped-key", "sk"},
	Short:   "Manage scoped signing keys",
	Long:    `Create, list, and delete scoped signing keys for accounts.`,
}

var signingKeyCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new scoped signing key",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyCreate,
}

var signingKeyListCmd = &cobra.Command{
	Use:   "list ACCOUNT_NAME",
	Short: "List scoped signing keys for an account",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyList,
}

var signingKeyGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get scoped signing key details",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyGet,
}

var signingKeyDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete a scoped signing key",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyDelete,
}

var (
	signingKeyOperatorID      string
	signingKeyAccountID       string
	signingKeyForce           bool
	signingKeyFromTemplate    string
	signingKeyTemplateVersion int
	signingKeyTrackLatest     bool
)

var signingKeyDetachTemplateCmd = &cobra.Command{
	Use:   "detach-template ID",
	Short: "Detach a scoped signing key from its template",
	Long: `Clear the SKK's template_id and template_version, leaving its
current permission columns untouched. After detach, the SKK becomes
standalone — future template updates have no effect on it, and the UI
stops rendering "From template X@vN" / outdated badges.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyDetachTemplate,
}

var signingKeyBumpTemplateCmd = &cobra.Command{
	Use:   "bump-template ID",
	Short: "Apply a template version to a scoped signing key",
	Long: `Snapshot a target template version's permissions into the SKK,
regenerate the parent account JWT, and push to every attached cluster.
When --to-version is unset, applies the template's current latest_version.
This is the explicit roll-out path — template updates never auto-cascade
to dependent SKKs.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyBumpTemplate,
}

var signingKeyBumpTargetVersion int
var signingKeyTrackLatestEnabled bool

var signingKeyTrackLatestCmd = &cobra.Command{
	Use:   "track-latest ID",
	Short: "Enable or disable auto-tracking of the bound template's latest version",
	Long: `Toggle the SKK's track_latest flag. When enabled, every UpdateTemplate
on the bound template auto-applies the new version to this SKK (re-signs
the parent account JWT, pushes to clusters). Enabling requires the SKK
to be templated AND clean (no drift); direct permission edits are
rejected while tracking is on so an auto-apply can't silently overwrite
operator changes.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyTrackLatest,
}

func init() {
	rootCmd.AddCommand(signingKeyCmd)

	signingKeyCmd.AddCommand(signingKeyCreateCmd)
	signingKeyCmd.AddCommand(signingKeyListCmd)
	signingKeyCmd.AddCommand(signingKeyGetCmd)
	signingKeyCmd.AddCommand(signingKeyDeleteCmd)
	signingKeyCmd.AddCommand(signingKeyDetachTemplateCmd)
	signingKeyCmd.AddCommand(signingKeyBumpTemplateCmd)
	signingKeyCmd.AddCommand(signingKeyTrackLatestCmd)
	signingKeyTrackLatestCmd.Flags().BoolVar(&signingKeyTrackLatestEnabled, "enabled", true, "true to enable tracking, false to disable")

	signingKeyCreateCmd.Flags().StringVar(&signingKeyOperatorID, "operator", "", "operator ID or name (required)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyAccountID, "account", "", "account name (required)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyFromTemplate, "from-template", "", "create the SKK from this operator-scoped template (snapshots its permissions)")
	signingKeyCreateCmd.Flags().IntVar(&signingKeyTemplateVersion, "template-version", 0, "pin to a specific template version (0 = current latest)")
	signingKeyCreateCmd.Flags().BoolVar(&signingKeyTrackLatest, "track-latest", false, "auto-apply every new template version to this SKK (requires --from-template; ignores --template-version)")
	_ = signingKeyCreateCmd.MarkFlagRequired("operator")
	_ = signingKeyCreateCmd.MarkFlagRequired("account")

	signingKeyListCmd.Flags().StringVar(&signingKeyOperatorID, "operator", "", "operator ID or name (required)")
	_ = signingKeyListCmd.MarkFlagRequired("operator")

	signingKeyDeleteCmd.Flags().BoolVarP(&signingKeyForce, "force", "f", false, "skip confirmation prompt")

	signingKeyBumpTemplateCmd.Flags().IntVar(&signingKeyBumpTargetVersion, "to-version", 0, "target template version (0 = current latest)")
}

func runSigningKeyCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(signingKeyOperatorID)
	if err != nil {
		return err
	}

	// Get account by name
	accountReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       signingKeyAccountID,
	})

	accountResp, err := GetClient().Account.GetAccountByName(context.Background(), accountReq)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	if signingKeyTrackLatest && signingKeyFromTemplate == "" {
		return fmt.Errorf("--track-latest requires --from-template")
	}
	createReq := &nisv1.CreateScopedSigningKeyRequest{
		AccountId:   accountResp.Msg.Account.Id,
		Name:        name,
		TrackLatest: signingKeyTrackLatest,
	}
	if signingKeyFromTemplate != "" {
		createReq.Template = &nisv1.TemplateRef{
			OperatorId:    operatorID,
			TemplateName:  signingKeyFromTemplate,
			VersionNumber: int32(signingKeyTemplateVersion),
		}
	}
	req := connect.NewRequest(createReq)

	resp, err := GetClient().ScopedSigningKey.CreateScopedSigningKey(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}

	printer.PrintSuccess("Scoped signing key created successfully")
	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyList(cmd *cobra.Command, args []string) error {
	accountName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(signingKeyOperatorID)
	if err != nil {
		return err
	}

	// Get account by name
	accountReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       accountName,
	})

	accountResp, err := GetClient().Account.GetAccountByName(context.Background(), accountReq)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	req := connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
		AccountId: accountResp.Msg.Account.Id,
	})

	resp, err := GetClient().ScopedSigningKey.ListScopedSigningKeys(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list scoped signing keys: %w", err)
	}

	if len(resp.Msg.Keys) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No scoped signing keys found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "ACCOUNT", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Keys))

		for i, key := range resp.Msg.Keys {
			createdAt := "-"
			if key.CreatedAt != nil {
				createdAt = key.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				key.Id[:8] + "...",
				key.Name,
				key.AccountId[:8] + "...",
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Keys)
}

func runSigningKeyGet(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{
		Id: id,
	})

	resp, err := GetClient().ScopedSigningKey.GetScopedSigningKey(context.Background(), req)
	if err != nil {
		return fmt.Errorf("scoped signing key not found: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyDelete(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	getReq := connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{
		Id: id,
	})

	getResp, err := GetClient().ScopedSigningKey.GetScopedSigningKey(context.Background(), getReq)
	if err != nil {
		return fmt.Errorf("scoped signing key not found: %w", err)
	}

	if !signingKeyForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("scoped signing key", getResp.Msg.Key.Name) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	req := connect.NewRequest(&nisv1.DeleteScopedSigningKeyRequest{
		Id: id,
	})

	_, err = GetClient().ScopedSigningKey.DeleteScopedSigningKey(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete scoped signing key: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Scoped signing key '%s' deleted successfully", getResp.Msg.Key.Name)
	}

	return nil
}

func runSigningKeyDetachTemplate(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().ScopedSigningKey.DetachFromTemplate(context.Background(),
		connect.NewRequest(&nisv1.DetachFromTemplateRequest{Id: id}))
	if err != nil {
		return fmt.Errorf("failed to detach scoped signing key from template: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}
	printer.PrintSuccess("Scoped signing key '%s' detached from template", resp.Msg.Key.Name)
	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyBumpTemplate(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Template.ApplyTemplateToScopedKey(context.Background(),
		connect.NewRequest(&nisv1.ApplyTemplateToScopedKeyRequest{
			ScopedSigningKeyId: id,
			VersionNumber:      int32(signingKeyBumpTargetVersion),
		}))
	if err != nil {
		return fmt.Errorf("failed to bump scoped signing key template: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}
	printer.PrintSuccess("Scoped signing key '%s' bumped to template version %d", resp.Msg.Key.Name, resp.Msg.Key.TemplateVersion)
	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyTrackLatest(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	resp, err := GetClient().ScopedSigningKey.SetTrackLatest(context.Background(),
		connect.NewRequest(&nisv1.SetTrackLatestRequest{
			Id:      id,
			Enabled: signingKeyTrackLatestEnabled,
		}))
	if err != nil {
		return fmt.Errorf("failed to set track_latest: %w", err)
	}
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}
	state := "disabled"
	if resp.Msg.Key.TrackLatest {
		state = "enabled"
	}
	printer.PrintSuccess("Scoped signing key '%s' track_latest %s", resp.Msg.Key.Name, state)
	return printer.PrintObject(resp.Msg.Key)
}
