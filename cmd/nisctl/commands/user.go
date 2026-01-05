package commands

import (
	"context"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage NATS users",
	Long:  `Create, list, update, and delete NATS users.`,
}

var userCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new user",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserCreate,
}

var userListCmd = &cobra.Command{
	Use:   "list ACCOUNT_NAME",
	Short: "List users for an account",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserList,
}

var userGetCmd = &cobra.Command{
	Use:   "get NAME",
	Short: "Get user details by name",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserGet,
}

var userCredsCmd = &cobra.Command{
	Use:   "creds NAME",
	Short: "Get user credentials file",
	Long:  `Get the NATS credentials file for a user. Output can be saved to a file.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runUserCreds,
}

var userDeleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "Delete a user by name",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserDelete,
}

var (
	userOperatorID      string
	userAccountID       string
	userDescription     string
	userScopedKeyID     string
	userCredsOutputFile string
	userForce           bool
)

func init() {
	rootCmd.AddCommand(userCmd)

	userCmd.AddCommand(userCreateCmd)
	userCmd.AddCommand(userListCmd)
	userCmd.AddCommand(userGetCmd)
	userCmd.AddCommand(userCredsCmd)
	userCmd.AddCommand(userDeleteCmd)

	// Create flags
	userCreateCmd.Flags().StringVar(&userOperatorID, "operator", "", "operator ID or name (required)")
	userCreateCmd.Flags().StringVar(&userAccountID, "account", "", "account name (required)")
	userCreateCmd.Flags().StringVar(&userDescription, "description", "", "user description")
	userCreateCmd.Flags().StringVar(&userScopedKeyID, "scoped-key", "", "scoped signing key ID (defines user permissions)")
	userCreateCmd.MarkFlagRequired("operator")
	userCreateCmd.MarkFlagRequired("account")

	// List flags
	userListCmd.Flags().StringVar(&userOperatorID, "operator", "", "operator ID or name (required)")
	userListCmd.MarkFlagRequired("operator")

	// Get flags
	userGetCmd.Flags().StringVar(&userOperatorID, "operator", "", "operator ID or name (required)")
	userGetCmd.Flags().StringVar(&userAccountID, "account", "", "account name (required)")
	userGetCmd.MarkFlagRequired("operator")
	userGetCmd.MarkFlagRequired("account")

	// Credentials flags
	userCredsCmd.Flags().StringVar(&userOperatorID, "operator", "", "operator ID or name (required)")
	userCredsCmd.Flags().StringVar(&userAccountID, "account", "", "account name (required)")
	userCredsCmd.Flags().StringVarP(&userCredsOutputFile, "output", "o", "", "output file (default: stdout)")
	userCredsCmd.MarkFlagRequired("operator")
	userCredsCmd.MarkFlagRequired("account")

	// Delete flags
	userDeleteCmd.Flags().StringVar(&userOperatorID, "operator", "", "operator ID or name (required)")
	userDeleteCmd.Flags().StringVar(&userAccountID, "account", "", "account name (required)")
	userDeleteCmd.Flags().BoolVarP(&userForce, "force", "f", false, "skip confirmation prompt")
	userDeleteCmd.MarkFlagRequired("operator")
	userDeleteCmd.MarkFlagRequired("account")
}

func runUserCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(userOperatorID)
	if err != nil {
		return err
	}

	// Get account by name
	accountReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       userAccountID,
	})

	accountResp, err := GetClient().Account.GetAccountByName(context.Background(), accountReq)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	req := connect.NewRequest(&nisv1.CreateUserRequest{
		AccountId:          accountResp.Msg.Account.Id,
		Name:               name,
		Description:        userDescription,
		ScopedSigningKeyId: userScopedKeyID,
	})

	// Note: Permissions are defined in the scoped signing key, not directly on the user

	resp, err := GetClient().User.CreateUser(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.User.Id)
		return nil
	}

	printer.PrintSuccess("User created successfully")
	return printer.PrintObject(resp.Msg.User)
}

func runUserList(cmd *cobra.Command, args []string) error {
	accountName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(userOperatorID)
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

	req := connect.NewRequest(&nisv1.ListUsersRequest{
		AccountId: accountResp.Msg.Account.Id,
	})

	resp, err := GetClient().User.ListUsers(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list users: %w", err)
	}

	if len(resp.Msg.Users) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No users found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "ACCOUNT", "SCOPED KEY", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Users))

		for i, user := range resp.Msg.Users {
			scopedKey := "-"
			if user.ScopedSigningKeyId != "" {
				scopedKey = user.ScopedSigningKeyId[:8] + "..."
			}

			createdAt := "-"
			if user.CreatedAt != nil {
				createdAt = user.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				user.Id[:8] + "...",
				user.Name,
				user.AccountId[:8] + "...",
				scopedKey,
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Users)
}

func runUserGet(cmd *cobra.Command, args []string) error {
	userName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator and account IDs
	accountID, err := resolveAccountForUser()
	if err != nil {
		return err
	}

	// Get user by name
	req := connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: accountID,
		Name:      userName,
	})

	resp, err := GetClient().User.GetUserByName(context.Background(), req)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.User.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.User)
}

func runUserCreds(cmd *cobra.Command, args []string) error {
	userName := args[0]

	// Resolve operator and account IDs
	accountID, err := resolveAccountForUser()
	if err != nil {
		return err
	}

	// Get user by name to get ID
	userReq := connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: accountID,
		Name:      userName,
	})

	userResp, err := GetClient().User.GetUserByName(context.Background(), userReq)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// Get credentials
	credsReq := connect.NewRequest(&nisv1.GetUserCredentialsRequest{
		Id: userResp.Msg.User.Id,
	})

	credsResp, err := GetClient().User.GetUserCredentials(context.Background(), credsReq)
	if err != nil {
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	// Output credentials
	if userCredsOutputFile != "" {
		// Write to file
		if err := os.WriteFile(userCredsOutputFile, []byte(credsResp.Msg.Credentials), 0600); err != nil {
			return fmt.Errorf("failed to write credentials file: %w", err)
		}
		if GetOutputFormat() != "quiet" {
			printer := client.NewPrinter(GetOutputFormat())
			printer.PrintSuccess("Credentials saved to %s", userCredsOutputFile)
		}
	} else {
		// Write to stdout
		fmt.Print(credsResp.Msg.Credentials)
	}

	return nil
}

func runUserDelete(cmd *cobra.Command, args []string) error {
	userName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator and account IDs
	accountID, err := resolveAccountForUser()
	if err != nil {
		return err
	}

	// Get user by name to get ID
	userReq := connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: accountID,
		Name:      userName,
	})

	userResp, err := GetClient().User.GetUserByName(context.Background(), userReq)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	userID := userResp.Msg.User.Id

	// Confirm deletion unless --force is used
	if !userForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("user", userName) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	// Delete the user
	req := connect.NewRequest(&nisv1.DeleteUserRequest{
		Id: userID,
	})

	_, err = GetClient().User.DeleteUser(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("User '%s' deleted successfully", userName)
	}

	return nil
}

// Helper function to resolve account ID for user commands (requires operator and account flags)
func resolveAccountForUser() (string, error) {
	// Resolve operator ID
	operatorID, err := resolveOperatorID(userOperatorID)
	if err != nil {
		return "", err
	}

	// Get account by name
	accountReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       userAccountID,
	})

	accountResp, err := GetClient().Account.GetAccountByName(context.Background(), accountReq)
	if err != nil {
		return "", fmt.Errorf("account not found: %w", err)
	}

	return accountResp.Msg.Account.Id, nil
}
