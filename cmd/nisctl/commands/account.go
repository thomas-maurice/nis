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

var (
	accountOperatorID   string
	accountDescription  string
	accountMaxMemory    int64
	accountMaxStorage   int64
	accountMaxStreams   int32
	accountMaxConsumers int32
	accountForce        bool
)

func init() {
	rootCmd.AddCommand(accountCmd)

	accountCmd.AddCommand(accountCreateCmd)
	accountCmd.AddCommand(accountListCmd)
	accountCmd.AddCommand(accountGetCmd)
	accountCmd.AddCommand(accountDeleteCmd)

	// Create flags
	accountCreateCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	accountCreateCmd.Flags().StringVar(&accountDescription, "description", "", "account description")
	accountCreateCmd.Flags().Int64Var(&accountMaxMemory, "max-memory", 0, "max memory (bytes)")
	accountCreateCmd.Flags().Int64Var(&accountMaxStorage, "max-storage", 0, "max storage (bytes)")
	accountCreateCmd.Flags().Int32Var(&accountMaxStreams, "max-streams", 0, "max streams")
	accountCreateCmd.Flags().Int32Var(&accountMaxConsumers, "max-consumers", 0, "max consumers")
	accountCreateCmd.MarkFlagRequired("operator")

	// Get flags
	accountGetCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	accountGetCmd.MarkFlagRequired("operator")

	// Delete flags
	accountDeleteCmd.Flags().StringVar(&accountOperatorID, "operator", "", "operator ID or name (required)")
	accountDeleteCmd.Flags().BoolVarP(&accountForce, "force", "f", false, "skip confirmation prompt")
	accountDeleteCmd.MarkFlagRequired("operator")
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
				acc.Id[:8] + "...",
				acc.Name,
				acc.OperatorId[:8] + "...",
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
