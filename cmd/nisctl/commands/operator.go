package commands

import (
	"context"
	"fmt"
	"time"

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

var operatorSetJWTPolicyCmd = &cobra.Command{
	Use:   "set-jwt-policy OPERATOR_ID_OR_NAME",
	Short: "Set JWT lifecycle policy for an operator",
	Long: `Configure per-operator JWT TTL and renewal defaults.

Duration flags accept Go duration syntax (e.g. 8760h for 1 year, 2160h for 90 days).
All flags are optional; only provided flags update the stored policy.`,
	Args: cobra.ExactArgs(1),
	RunE: runOperatorSetJWTPolicy,
}

var operatorRunJWTSweepCmd = &cobra.Command{
	Use:   "run-jwt-sweep",
	Short: "Trigger an immediate JWT expiry sweep (admin only)",
	Args:  cobra.NoArgs,
	RunE:  runOperatorRunJWTSweep,
}

var (
	operatorSystemAccountPubKey string
	operatorDescription         string
	operatorForce               bool

	// list flags
	operatorListNameLike string
	operatorListLimit    int32
	operatorListCursor   string

	// set-jwt-policy flags
	jwtPolicyUserTTL    string
	jwtPolicyAccountTTL string
	jwtPolicyWarnWindow string
	jwtPolicyAutoRenew  bool
)

func init() {
	rootCmd.AddCommand(operatorCmd)

	operatorCmd.AddCommand(operatorCreateCmd)
	operatorCmd.AddCommand(operatorListCmd)
	operatorCmd.AddCommand(operatorGetCmd)
	operatorCmd.AddCommand(operatorDeleteCmd)
	operatorCmd.AddCommand(operatorSetSystemAccountCmd)
	operatorCmd.AddCommand(operatorGenerateIncludeCmd)
	operatorCmd.AddCommand(operatorSetJWTPolicyCmd)
	operatorCmd.AddCommand(operatorRunJWTSweepCmd)

	// Create flags
	operatorCreateCmd.Flags().StringVar(&operatorDescription, "description", "", "operator description")

	// Set system account flags
	operatorSetSystemAccountCmd.Flags().StringVar(&operatorSystemAccountPubKey, "system-account-pubkey", "", "system account public key (required)")
	_ = operatorSetSystemAccountCmd.MarkFlagRequired("system-account-pubkey")

	// List flags
	operatorListCmd.Flags().StringVar(&operatorListNameLike, "name-like", "", "filter by name (substring match)")
	operatorListCmd.Flags().Int32Var(&operatorListLimit, "limit", 0, "page size (0 = fetch all pages)")
	operatorListCmd.Flags().StringVar(&operatorListCursor, "cursor", "", "start at this pagination cursor (implies single-page mode)")

	// Delete flags
	operatorDeleteCmd.Flags().BoolVarP(&operatorForce, "force", "f", false, "skip confirmation prompt")

	// set-jwt-policy flags
	operatorSetJWTPolicyCmd.Flags().StringVar(&jwtPolicyUserTTL, "user-ttl", "", "user JWT TTL as Go duration (e.g. 8760h = 1 year, 2160h = 90 days)")
	operatorSetJWTPolicyCmd.Flags().StringVar(&jwtPolicyAccountTTL, "account-ttl", "", "account JWT TTL as Go duration (e.g. 8760h = 1 year)")
	operatorSetJWTPolicyCmd.Flags().StringVar(&jwtPolicyWarnWindow, "warn-window", "", "warn-before-expiry window as Go duration (e.g. 168h = 1 week)")
	operatorSetJWTPolicyCmd.Flags().BoolVar(&jwtPolicyAutoRenew, "auto-renew", false, "enable automatic JWT renewal before expiry")
}

func runOperatorCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:        name,
		Description: operatorDescription,
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

	// Single-page mode when --limit or --cursor is explicitly set.
	singlePage := cmd.Flags().Changed("limit") || cmd.Flags().Changed("cursor")

	var allOperators []*nisv1.Operator
	cursor := operatorListCursor
	pageNum := 0

	for {
		pageLimit := operatorListLimit
		if !singlePage {
			pageLimit = 200
		}

		req := connect.NewRequest(&nisv1.ListOperatorsRequest{
			NameLike: operatorListNameLike,
			Page: &nisv1.PageRequest{
				Limit:  pageLimit,
				Cursor: cursor,
			},
		})

		resp, err := GetClient().Operator.ListOperators(context.Background(), req)
		if err != nil {
			return fmt.Errorf("failed to list operators: %w", err)
		}

		allOperators = append(allOperators, resp.Msg.Operators...)
		pageNum++

		if singlePage {
			// Print next cursor to stderr for table output so stdout stays pipeable.
			if resp.Msg.NextCursor != "" && GetOutputFormat() == "table" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next-cursor: %s\n", resp.Msg.NextCursor)
			}
			break
		}

		if resp.Msg.NextCursor == "" {
			break
		}
		if pageNum > 1 && GetOutputFormat() == "table" {
			fmt.Fprintf(cmd.ErrOrStderr(), "fetching page %d...\n", pageNum+1)
		}
		cursor = resp.Msg.NextCursor
	}

	if len(allOperators) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No operators found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "SYSTEM ACCOUNT", "CREATED AT"}
		rows := make([][]string, len(allOperators))

		for i, op := range allOperators {
			systemAccount := "-"
			if op.SystemAccountPubKey != "" {
				systemAccount = client.AccountKey(op.SystemAccountPubKey)
			}

			createdAt := "-"
			if op.CreatedAt != nil {
				createdAt = op.CreatedAt.AsTime().Local().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				client.OperatorID(op.Id),
				op.Name,
				systemAccount,
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(allOperators)
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

func runOperatorSetJWTPolicy(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	operatorID, err := resolveOperatorID(args[0])
	if err != nil {
		return err
	}

	req := &nisv1.SetJWTPolicyRequest{Id: operatorID}

	if cmd.Flags().Changed("user-ttl") {
		d, err := time.ParseDuration(jwtPolicyUserTTL)
		if err != nil {
			return fmt.Errorf("invalid --user-ttl: %w", err)
		}
		v := int64(d.Seconds())
		req.UserJwtTtlSeconds = &v
	}

	if cmd.Flags().Changed("account-ttl") {
		d, err := time.ParseDuration(jwtPolicyAccountTTL)
		if err != nil {
			return fmt.Errorf("invalid --account-ttl: %w", err)
		}
		v := int64(d.Seconds())
		req.AccountJwtTtlSeconds = &v
	}

	if cmd.Flags().Changed("warn-window") {
		d, err := time.ParseDuration(jwtPolicyWarnWindow)
		if err != nil {
			return fmt.Errorf("invalid --warn-window: %w", err)
		}
		v := int64(d.Seconds())
		req.JwtWarnWindowSeconds = &v
	}

	if cmd.Flags().Changed("auto-renew") {
		v := jwtPolicyAutoRenew
		req.JwtAutoRenew = &v
	}

	resp, err := GetClient().Operator.SetJWTPolicy(context.Background(), connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to set JWT policy: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Operator.Id)
		return nil
	}

	printer.PrintSuccess("JWT policy updated")
	return printer.PrintObject(resp.Msg.Operator)
}

func runOperatorRunJWTSweep(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Operator.RunJWTExpirySweep(context.Background(), connect.NewRequest(&nisv1.RunJWTExpirySweepRequest{}))
	if err != nil {
		return fmt.Errorf("failed to run JWT expiry sweep: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		return nil
	}

	r := resp.Msg
	printer.PrintSuccess("JWT expiry sweep complete")
	printer.PrintMessage("  revocations_pruned:    %d", r.RevocationsPruned)
	printer.PrintMessage("  expiring_soon_emitted: %d", r.ExpiringSoonEmitted)
	printer.PrintMessage("  expired_alerts_emitted:%d", r.ExpiredAlertsEmitted)
	printer.PrintMessage("  auto_renewed:          %d", r.AutoRenewed)
	return nil
}
