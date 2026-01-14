package commands

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Manage NATS operators",
	Long:  `Create, list, update, and delete NATS operators.`,
}

var operatorCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorCreate,
}

var operatorListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all operators",
	RunE:  runOperatorList,
}

var operatorGetCmd = &cobra.Command{
	Use:   "get ID_OR_NAME",
	Short: "Get operator details",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorGet,
}

var operatorDeleteCmd = &cobra.Command{
	Use:   "delete ID_OR_NAME",
	Short: "Delete an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorDelete,
}

var operatorSetSystemAccountCmd = &cobra.Command{
	Use:   "set-system-account OPERATOR_ID_OR_NAME",
	Short: "Set the system account for an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorSetSystemAccount,
}

var operatorGenerateIncludeCmd = &cobra.Command{
	Use:   "generate-include OPERATOR_ID_OR_NAME",
	Short: "Generate NATS operator include configuration",
	Long:  `Generates a NATS server configuration file with the operator JWT and preloaded system account.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runOperatorGenerateInclude,
}

var (
	operatorSystemAccountPubKey string
	operatorDescription         string
	operatorForce               bool
)

func init() {
	rootCmd.AddCommand(operatorCmd)

	operatorCmd.AddCommand(operatorCreateCmd)
	operatorCmd.AddCommand(operatorListCmd)
	operatorCmd.AddCommand(operatorGetCmd)
	operatorCmd.AddCommand(operatorDeleteCmd)
	operatorCmd.AddCommand(operatorSetSystemAccountCmd)
	operatorCmd.AddCommand(operatorGenerateIncludeCmd)

	// Create flags
	operatorCreateCmd.Flags().StringVar(&operatorSystemAccountPubKey, "system-account-pubkey", "", "system account public key")
	operatorCreateCmd.Flags().StringVar(&operatorDescription, "description", "", "operator description")

	// Set system account flags
	operatorSetSystemAccountCmd.Flags().StringVar(&operatorSystemAccountPubKey, "system-account-pubkey", "", "system account public key (required)")
	operatorSetSystemAccountCmd.MarkFlagRequired("system-account-pubkey")

	// Delete flags
	operatorDeleteCmd.Flags().BoolVarP(&operatorForce, "force", "f", false, "skip confirmation prompt")
}

func runOperatorCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:                name,
		Description:         operatorDescription,
		SystemAccountPubKey: operatorSystemAccountPubKey,
	})

	resp, err := GetClient().Operator.CreateOperator(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create operator: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Operator.Id)
		return nil
	}

	printer.PrintSuccess("Operator created successfully")
	return printer.PrintObject(resp.Msg.Operator)
}

func runOperatorList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.ListOperatorsRequest{})

	resp, err := GetClient().Operator.ListOperators(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list operators: %w", err)
	}

	if len(resp.Msg.Operators) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No operators found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "SYSTEM ACCOUNT", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Operators))

		for i, op := range resp.Msg.Operators {
			systemAccount := "-"
			if op.SystemAccountPubKey != "" {
				systemAccount = op.SystemAccountPubKey[:12] + "..."
			}

			createdAt := "-"
			if op.CreatedAt != nil {
				createdAt = op.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				op.Id[:8] + "...",
				op.Name,
				systemAccount,
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Operators)
}

func runOperatorGet(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Try to get by ID first
	req := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: idOrName,
	})

	resp, err := GetClient().Operator.GetOperator(context.Background(), req)
	if err != nil {
		// Try by name if ID lookup failed
		nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name: idOrName,
		})

		nameResp, nameErr := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("operator not found: %w", nameErr)
		}

		resp = &connect.Response[nisv1.GetOperatorResponse]{
			Msg: &nisv1.GetOperatorResponse{
				Operator: nameResp.Msg.Operator,
			},
		}
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Operator.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.Operator)
}

func runOperatorDelete(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Get operator details first to confirm name
	var operatorID, operatorName string

	// Try to get by ID first
	getReq := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: idOrName,
	})

	getResp, err := GetClient().Operator.GetOperator(context.Background(), getReq)
	if err != nil {
		// Try by name if ID lookup failed
		nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name: idOrName,
		})

		nameResp, nameErr := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("operator not found: %w", nameErr)
		}

		operatorID = nameResp.Msg.Operator.Id
		operatorName = nameResp.Msg.Operator.Name
	} else {
		operatorID = getResp.Msg.Operator.Id
		operatorName = getResp.Msg.Operator.Name
	}

	// Confirm deletion unless --force is used
	if !operatorForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("operator", operatorName) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	// Delete the operator
	req := connect.NewRequest(&nisv1.DeleteOperatorRequest{
		Id: operatorID,
	})

	_, err = GetClient().Operator.DeleteOperator(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete operator: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Operator '%s' deleted successfully", operatorName)
	}

	return nil
}

func runOperatorSetSystemAccount(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Get operator ID first (resolve name if needed)
	var operatorID string

	getReq := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: idOrName,
	})

	getResp, err := GetClient().Operator.GetOperator(context.Background(), getReq)
	if err != nil {
		// Try by name if ID lookup failed
		nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name: idOrName,
		})

		nameResp, nameErr := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("operator not found: %w", nameErr)
		}

		operatorID = nameResp.Msg.Operator.Id
	} else {
		operatorID = getResp.Msg.Operator.Id
	}

	// Set system account
	req := connect.NewRequest(&nisv1.SetSystemAccountRequest{
		Id:                  operatorID,
		SystemAccountPubKey: operatorSystemAccountPubKey,
	})

	resp, err := GetClient().Operator.SetSystemAccount(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to set system account: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Operator.Id)
		return nil
	}

	printer.PrintSuccess("System account set successfully")
	return printer.PrintObject(resp.Msg.Operator)
}

func runOperatorGenerateInclude(cmd *cobra.Command, args []string) error {
	idOrName := args[0]

	// Get operator
	var operator *nisv1.Operator

	getReq := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: idOrName,
	})

	getResp, err := GetClient().Operator.GetOperator(context.Background(), getReq)
	if err != nil {
		// Try by name if ID lookup failed
		nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name: idOrName,
		})

		nameResp, nameErr := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("operator not found: %w", nameErr)
		}

		operator = nameResp.Msg.Operator
	} else {
		operator = getResp.Msg.Operator
	}

	// Check if operator has system account configured
	if operator.SystemAccountPubKey == "" {
		return fmt.Errorf("operator does not have a system account configured. Use 'nisctl operator set-system-account' first")
	}

	// Get all accounts for this operator and find the system account
	listReq := connect.NewRequest(&nisv1.ListAccountsRequest{
		OperatorId: operator.Id,
		Options: &nisv1.ListOptions{
			Limit: 1000,
		},
	})

	listResp, err := GetClient().Account.ListAccounts(context.Background(), listReq)
	if err != nil {
		return fmt.Errorf("failed to list accounts: %w", err)
	}

	// Find the system account by public key
	var sysAccount *nisv1.Account
	for _, account := range listResp.Msg.Accounts {
		if account.PublicKey == operator.SystemAccountPubKey {
			sysAccount = account
			break
		}
	}

	if sysAccount == nil {
		return fmt.Errorf("system account not found with public key: %s", operator.SystemAccountPubKey)
	}

	// Generate NATS config
	config := fmt.Sprintf(`# NATS Server Configuration with JWT Authentication
# Generated by nisctl for operator: %s

# Operator JWT
operator: %s

# File resolver - supports dynamic updates via $SYS.REQ.CLAIMS.UPDATE
resolver: {
    type: full
    dir: '/resolver'
    allow_delete: true
    interval: "2m"
}

# Preload system account (%s)
resolver_preload: {
    %s: %s
}

# JetStream configuration
jetstream: {
    store_dir: /data/jetstream
}
`, operator.Name, operator.Jwt, sysAccount.Name, operator.SystemAccountPubKey, sysAccount.Jwt)

	// Output the configuration
	fmt.Print(config)

	return nil
}
