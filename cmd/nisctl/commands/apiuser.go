package commands

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var apiUserCmd = &cobra.Command{
	Use:   "api-user",
	Short: "Manage API users",
	Long:  `Create, list, and delete API users for authentication.`,
}

var apiUserCreateCmd = &cobra.Command{
	Use:   "create USERNAME",
	Short: "Create a new API user",
	Args:  cobra.ExactArgs(1),
	RunE:  runAPIUserCreate,
}

var apiUserListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API users",
	RunE:  runAPIUserList,
}

var apiUserGetCmd = &cobra.Command{
	Use:   "get ID_OR_USERNAME",
	Short: "Get API user details",
	Args:  cobra.ExactArgs(1),
	RunE:  runAPIUserGet,
}

var apiUserDeleteCmd = &cobra.Command{
	Use:   "delete ID_OR_USERNAME",
	Short: "Delete an API user",
	Args:  cobra.ExactArgs(1),
	RunE:  runAPIUserDelete,
}

var (
	apiUserPassword string
	apiUserRole     string
	apiUserForce    bool
)

func init() {
	rootCmd.AddCommand(apiUserCmd)

	apiUserCmd.AddCommand(apiUserCreateCmd)
	apiUserCmd.AddCommand(apiUserListCmd)
	apiUserCmd.AddCommand(apiUserGetCmd)
	apiUserCmd.AddCommand(apiUserDeleteCmd)

	apiUserCreateCmd.Flags().StringVarP(&apiUserPassword, "password", "p", "", "password (will prompt if not provided)")
	apiUserCreateCmd.Flags().StringVarP(&apiUserRole, "role", "r", "admin", "role (admin, operator-admin, account-admin)")

	apiUserDeleteCmd.Flags().BoolVarP(&apiUserForce, "force", "f", false, "skip confirmation prompt")
}

func runAPIUserCreate(cmd *cobra.Command, args []string) error {
	username := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	password := apiUserPassword
	if password == "" {
		var err error
		password, err = client.PromptPassword("Password: ")
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}

		confirmPassword, err := client.PromptPassword("Confirm password: ")
		if err != nil {
			return fmt.Errorf("failed to read confirmation password: %w", err)
		}

		if password != confirmPassword {
			return fmt.Errorf("passwords do not match")
		}
	}

	req := connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    username,
		Password:    password,
		Permissions: []string{apiUserRole},
	})

	resp, err := GetClient().Auth.CreateAPIUser(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create API user: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.User.Id)
		return nil
	}

	printer.PrintSuccess("API user created successfully")
	return printer.PrintObject(resp.Msg.User)
}

func runAPIUserList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.ListAPIUsersRequest{})

	resp, err := GetClient().Auth.ListAPIUsers(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list API users: %w", err)
	}

	if len(resp.Msg.Users) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No API users found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "USERNAME", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Users))

		for i, user := range resp.Msg.Users {
			createdAt := "-"
			if user.CreatedAt != nil {
				createdAt = user.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				user.Id[:8] + "...",
				user.Username,
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Users)
}

func runAPIUserGet(cmd *cobra.Command, args []string) error {
	idOrUsername := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.GetAPIUserRequest{
		Id: idOrUsername,
	})

	resp, err := GetClient().Auth.GetAPIUser(context.Background(), req)
	if err != nil {
		nameReq := connect.NewRequest(&nisv1.GetAPIUserByUsernameRequest{
			Username: idOrUsername,
		})

		nameResp, nameErr := GetClient().Auth.GetAPIUserByUsername(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("API user not found: %w", nameErr)
		}

		resp = &connect.Response[nisv1.GetAPIUserResponse]{
			Msg: &nisv1.GetAPIUserResponse{
				User: nameResp.Msg.User,
			},
		}
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.User.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.User)
}

func runAPIUserDelete(cmd *cobra.Command, args []string) error {
	idOrUsername := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	var apiUserID, username string

	getReq := connect.NewRequest(&nisv1.GetAPIUserRequest{
		Id: idOrUsername,
	})

	getResp, err := GetClient().Auth.GetAPIUser(context.Background(), getReq)
	if err != nil {
		nameReq := connect.NewRequest(&nisv1.GetAPIUserByUsernameRequest{
			Username: idOrUsername,
		})

		nameResp, nameErr := GetClient().Auth.GetAPIUserByUsername(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("API user not found: %w", nameErr)
		}

		apiUserID = nameResp.Msg.User.Id
		username = nameResp.Msg.User.Username
	} else {
		apiUserID = getResp.Msg.User.Id
		username = getResp.Msg.User.Username
	}

	if !apiUserForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("API user", username) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	req := connect.NewRequest(&nisv1.DeleteAPIUserRequest{
		Id: apiUserID,
	})

	_, err = GetClient().Auth.DeleteAPIUser(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete API user: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("API user '%s' deleted successfully", username)
	}

	return nil
}
