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
	signingKeyOperatorID string
	signingKeyAccountID  string
	signingKeyForce      bool
)

func init() {
	rootCmd.AddCommand(signingKeyCmd)

	signingKeyCmd.AddCommand(signingKeyCreateCmd)
	signingKeyCmd.AddCommand(signingKeyListCmd)
	signingKeyCmd.AddCommand(signingKeyGetCmd)
	signingKeyCmd.AddCommand(signingKeyDeleteCmd)

	signingKeyCreateCmd.Flags().StringVar(&signingKeyOperatorID, "operator", "", "operator ID or name (required)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyAccountID, "account", "", "account name (required)")
	signingKeyCreateCmd.MarkFlagRequired("operator")
	signingKeyCreateCmd.MarkFlagRequired("account")

	signingKeyListCmd.Flags().StringVar(&signingKeyOperatorID, "operator", "", "operator ID or name (required)")
	signingKeyListCmd.MarkFlagRequired("operator")

	signingKeyDeleteCmd.Flags().BoolVarP(&signingKeyForce, "force", "f", false, "skip confirmation prompt")
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

	req := connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accountResp.Msg.Account.Id,
		Name:      name,
	})

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
