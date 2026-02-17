package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage API users",
	Long:  `Manage API users for authenticating to the NATS Identity Service.`,
}

var userCreateCmd = &cobra.Command{
	Use:   "create USERNAME",
	Short: "Create a new API user",
	Long: `Create a new API user with a username, password, and role.

Roles:
  - admin: Full access to all operations
  - operator-admin: Can manage operators, accounts, users, and scoped keys
  - account-admin: Can manage accounts and users`,
	Args: cobra.ExactArgs(1),
	RunE: runUserCreate,
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API users",
	Long:  `List all API users in the system.`,
	RunE:  runUserList,
}

func init() {
	rootCmd.AddCommand(userCmd)
	userCmd.AddCommand(userCreateCmd)
	userCmd.AddCommand(userListCmd)

	// Flags for user create
	userCreateCmd.Flags().String("password", "", "password for the user (required)")
	userCreateCmd.Flags().String("role", "operator-admin", "role for the user (admin, operator-admin, account-admin)")
	_ = userCreateCmd.MarkFlagRequired("password")

	// Database flags for user commands
	for _, cmd := range []*cobra.Command{userCreateCmd, userListCmd} {
		cmd.Flags().String("db-driver", "sqlite", "database driver (sqlite or postgres)")
		cmd.Flags().String("db-dsn", "nis.db", "database connection string")

		_ = viper.BindPFlag("database.driver", cmd.Flags().Lookup("db-driver"))
		_ = viper.BindPFlag("database.dsn", cmd.Flags().Lookup("db-dsn"))
	}
}

func runUserCreate(cmd *cobra.Command, args []string) error {
	username := args[0]
	password, _ := cmd.Flags().GetString("password")
	roleStr, _ := cmd.Flags().GetString("role")

	// Validate role
	role := entities.APIUserRole(roleStr)
	if !role.IsValid() {
		return fmt.Errorf("invalid role: %s (must be admin, operator-admin, or account-admin)", roleStr)
	}

	// Create repository factory and connect
	repoFactory, err := createRepositoryFactory()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = repoFactory.Close() }()

	// Initialize service
	authService := services.NewAuthService(
		repoFactory.APIUserRepository(),
		"dummy-secret", // JWT secret not needed for user creation
		0,
	)

	// Create a system admin user for CLI operations (bypasses permission checks)
	systemAdmin := &entities.APIUser{
		Role: entities.RoleAdmin,
	}

	// Create user
	user, err := authService.CreateAPIUser(ctx, services.CreateAPIUserRequest{
		Username: username,
		Password: password,
		Role:     role,
	}, systemAdmin)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	fmt.Printf("✓ API user created successfully\n")
	fmt.Printf("  ID:       %s\n", user.ID)
	fmt.Printf("  Username: %s\n", user.Username)
	fmt.Printf("  Role:     %s\n", user.Role)
	fmt.Printf("  Created:  %s\n", user.CreatedAt.Format("2006-01-02 15:04:05"))

	return nil
}

func runUserList(cmd *cobra.Command, args []string) error {
	// Create repository factory and connect
	repoFactory, err := createRepositoryFactory()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = repoFactory.Close() }()

	// Initialize service
	authService := services.NewAuthService(
		repoFactory.APIUserRepository(),
		"dummy-secret", // JWT secret not needed for listing
		0,
	)

	// Create a system admin user for CLI operations (bypasses permission checks)
	systemAdmin := &entities.APIUser{
		Role: entities.RoleAdmin,
	}

	// List users
	users, err := authService.ListAPIUsers(ctx, systemAdmin)
	if err != nil {
		return fmt.Errorf("failed to list users: %w", err)
	}

	if len(users) == 0 {
		fmt.Println("No API users found")
		return nil
	}

	// Print users in a table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tUSERNAME\tROLE\tCREATED")
	_, _ = fmt.Fprintln(w, "--\t--------\t----\t-------")

	for _, user := range users {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			user.ID.String()[:8]+"...",
			user.Username,
			user.Role,
			user.CreatedAt.Format("2006-01-02 15:04:05"),
		)
	}

	_ = w.Flush()

	return nil
}
