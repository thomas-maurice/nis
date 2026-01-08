package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

var repairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Repair operations for the database",
	Long:  `Perform various repair operations on the database to fix inconsistencies.`,
}

var repairSigningKeysCmd = &cobra.Command{
	Use:   "signing-keys",
	Short: "Add default signing keys to accounts that don't have any",
	Long: `This command scans all accounts and creates a default scoped signing key
for any account that doesn't have at least one signing key. This is useful
for repairing accounts created before the mandatory signing key feature.`,
	RunE: runRepairSigningKeys,
}

func init() {
	rootCmd.AddCommand(repairCmd)
	repairCmd.AddCommand(repairSigningKeysCmd)

	// Database flags
	repairSigningKeysCmd.Flags().String("db-driver", "sqlite", "database driver (sqlite or postgres)")
	repairSigningKeysCmd.Flags().String("db-dsn", "nis.db", "database connection string")
	repairSigningKeysCmd.Flags().String("encryption-key", "", "encryption key for sensitive data")

	viper.BindPFlag("database.driver", repairSigningKeysCmd.Flags().Lookup("db-driver"))
	viper.BindPFlag("database.dsn", repairSigningKeysCmd.Flags().Lookup("db-dsn"))
	viper.BindPFlag("encryption.key", repairSigningKeysCmd.Flags().Lookup("encryption-key"))
}

func runRepairSigningKeys(cmd *cobra.Command, args []string) error {
	dbDriver := viper.GetString("database.driver")
	dbDSN := viper.GetString("database.dsn")

	// Initialize encryption service
	encryptor, err := initEncryptionService()
	if err != nil {
		return fmt.Errorf("failed to initialize encryption: %w", err)
	}

	// Create repository factory
	repoFactory, err := persistence.NewRepositoryFactory(persistence.Config{
		Driver: dbDriver,
		DSN:    dbDSN,
	})
	if err != nil {
		return fmt.Errorf("failed to create repository factory: %w", err)
	}

	// Connect to database
	ctx := context.Background()
	if err := repoFactory.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer repoFactory.Close()

	// Get all accounts
	accounts, err := repoFactory.AccountRepository().List(ctx, repositories.ListOptions{
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		return fmt.Errorf("failed to list accounts: %w", err)
	}

	fmt.Printf("Found %d accounts, checking for missing signing keys...\n", len(accounts))

	repaired := 0
	for _, account := range accounts {
		// Check if account has any signing keys
		keys, err := repoFactory.ScopedSigningKeyRepository().ListByAccount(ctx, account.ID, repositories.ListOptions{
			Limit:  10,
			Offset: 0,
		})
		if err != nil {
			fmt.Printf("Warning: failed to list signing keys for account %s: %v\n", account.Name, err)
			continue
		}

		if len(keys) > 0 {
			fmt.Printf("  Account '%s' already has %d signing key(s), skipping\n", account.Name, len(keys))
			continue
		}

		fmt.Printf("  Account '%s' has no signing keys, creating default key...\n", account.Name)

		// Create default signing key
		seed, pubKey, err := services.GenerateNKey(nkeys.PrefixByteAccount)
		if err != nil {
			fmt.Printf("  Error: failed to generate key for account %s: %v\n", account.Name, err)
			continue
		}

		encryptedSeed, err := encryptor.Encrypt(ctx, seed)
		if err != nil {
			fmt.Printf("  Error: failed to encrypt key for account %s: %v\n", account.Name, err)
			continue
		}

		scopedKey := &entities.ScopedSigningKey{
			ID:              uuid.New(),
			AccountID:       account.ID,
			Name:            "default",
			Description:     "Default scoped signing key with unlimited account permissions (created by repair)",
			EncryptedSeed:   encryptedSeed,
			PublicKey:       pubKey,
			PubAllow:        []string{},
			PubDeny:         []string{},
			SubAllow:        []string{},
			SubDeny:         []string{},
			ResponseMaxMsgs: 0,
			ResponseTTL:     0,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}

		if err := repoFactory.ScopedSigningKeyRepository().Create(ctx, scopedKey); err != nil {
			fmt.Printf("  Error: failed to create signing key for account %s: %v\n", account.Name, err)
			continue
		}

		fmt.Printf("  ✓ Created default signing key for account '%s'\n", account.Name)
		repaired++
	}

	fmt.Printf("\nRepair complete: created default signing keys for %d account(s)\n", repaired)
	return nil
}
