package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Database migration commands",
	Long:  `Manage database migrations for the NATS Identity Service.`,
}

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Run database migrations",
	Long:  `Run all pending database migrations to bring the schema up to date.`,
	RunE:  runMigrateUp,
}

var migrateDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Rollback database migrations",
	Long: `Rollback database migrations. WARNING: This will drop all tables
and may result in data loss. Use with caution.`,
	RunE: runMigrateDown,
}

var migrateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show migration status",
	Long:  `Show the current status of database migrations.`,
	RunE:  runMigrateStatus,
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.AddCommand(migrateUpCmd)
	migrateCmd.AddCommand(migrateDownCmd)
	migrateCmd.AddCommand(migrateStatusCmd)

	// Database flags for migrate commands
	for _, cmd := range []*cobra.Command{migrateUpCmd, migrateDownCmd, migrateStatusCmd} {
		cmd.Flags().String("db-driver", "sqlite", "database driver (sqlite or postgres)")
		cmd.Flags().String("db-dsn", "nis.db", "database connection string")

		_ = viper.BindPFlag("database.driver", cmd.Flags().Lookup("db-driver"))
		_ = viper.BindPFlag("database.dsn", cmd.Flags().Lookup("db-dsn"))
	}
}

func runMigrateUp(cmd *cobra.Command, args []string) error {
	repoFactory, err := createRepositoryFactory()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = repoFactory.Close() }()

	fmt.Println("Running database migrations...")

	if err := repoFactory.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	fmt.Println("✓ Migrations completed successfully")
	return nil
}

func runMigrateDown(cmd *cobra.Command, args []string) error {
	repoFactory, err := createRepositoryFactory()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = repoFactory.Close() }()

	fmt.Println("Rolling back database migrations...")
	fmt.Println("WARNING: This will revert the last migration!")

	if err := repoFactory.Rollback(ctx); err != nil {
		return fmt.Errorf("failed to rollback migration: %w", err)
	}

	fmt.Println("✓ Migration rolled back successfully")
	return nil
}

func runMigrateStatus(cmd *cobra.Command, args []string) error {
	repoFactory, err := createRepositoryFactory()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = repoFactory.Close() }()

	fmt.Println("Database Migration Status:")
	fmt.Println()

	migrations, err := repoFactory.MigrationStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get migration status: %w", err)
	}

	if len(migrations) == 0 {
		fmt.Println("No migrations found")
		return nil
	}

	fmt.Println("    Applied At                  Migration")
	fmt.Println("    =======================================")
	for _, m := range migrations {
		status := "Pending..."
		if m.AppliedAt != nil {
			status = *m.AppliedAt
		}
		fmt.Printf("    %-30s -- %s\n", status, m.Name)
	}

	return nil
}

func createRepositoryFactory() (persistence.RepositoryFactory, error) {
	driver := viper.GetString("database.driver")
	dsn := viper.GetString("database.dsn")

	return persistence.NewRepositoryFactory(persistence.Config{
		Driver: driver,
		DSN:    dsn,
	})
}
