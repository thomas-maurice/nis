package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/thomas-maurice/nis/internal/application/services"
	"github.com/thomas-maurice/nis/internal/config"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
)

func main() {
	ctx := context.Background()

	// Create database connection
	db, err := sql.NewDB(config.DatabaseConfig{
		Driver: "sqlite",
		Path:   "nis.db",
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Create encryptor
	encryptor, err := encryption.NewChaChaEncryptor(map[string]string{
		"default": "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=",
	}, "default")
	if err != nil {
		log.Fatalf("Failed to create encryptor: %v", err)
	}

	// Create repositories
	clusterRepo := sql.NewClusterRepo(db)
	userRepo := sql.NewUserRepo(db)
	accountRepo := sql.NewAccountRepo(db)
	operatorRepo := sql.NewOperatorRepo(db)

	// Create JWT service
	jwtService := services.NewJWTService(encryptor)

	// Get lil cluster
	clusters, err := clusterRepo.List(ctx, repositories.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list clusters: %v", err)
	}

	var lilCluster *uuid.UUID
	for _, cluster := range clusters {
		if cluster.Name == "lil" {
			lilCluster = &cluster.ID
			fmt.Printf("Found lil cluster: %s\n", cluster.ID)
			fmt.Printf("  Server URLs: %v\n", cluster.ServerURLs)
			fmt.Printf("  Encrypted creds length: %d\n", len(cluster.EncryptedCreds))
			break
		}
	}

	if lilCluster == nil {
		log.Fatal("lil cluster not found")
	}

	// Get cluster details
	cluster, err := clusterRepo.GetByID(ctx, *lilCluster)
	if err != nil {
		log.Fatalf("Failed to get cluster: %v", err)
	}

	if cluster.EncryptedCreds == "" {
		log.Fatal("Cluster has no credentials configured")
	}

	// Decrypt credentials
	credsBytes, err := encryptor.Decrypt(ctx, cluster.EncryptedCreds)
	if err != nil {
		log.Fatalf("Failed to decrypt credentials: %v", err)
	}
	creds := string(credsBytes)

	fmt.Printf("\n=== CREDENTIALS ===\n")
	fmt.Printf("%s\n", creds)
	fmt.Printf("===================\n\n")

	// Get the system user to inspect the JWT
	operator, err := operatorRepo.GetByID(ctx, cluster.OperatorID)
	if err != nil {
		log.Fatalf("Failed to get operator: %v", err)
	}

	accounts, err := accountRepo.ListByOperator(ctx, operator.ID, repositories.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list accounts: %v", err)
	}

	var sysAccount *uuid.UUID
	for _, account := range accounts {
		if account.PublicKey == operator.SystemAccountPubKey {
			sysAccount = &account.ID
			fmt.Printf("System Account: %s (ID: %s)\n", account.Name, account.ID)
			fmt.Printf("  Public Key: %s\n", account.PublicKey)
			fmt.Printf("  JWT: %s...\n", account.JWT[:50])
			break
		}
	}

	if sysAccount == nil {
		log.Fatal("System account not found")
	}

	users, err := userRepo.ListByAccount(ctx, *sysAccount, repositories.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list users: %v", err)
	}

	for _, user := range users {
		if user.Name == "system" {
			fmt.Printf("\nSystem User: %s (ID: %s)\n", user.Name, user.ID)
			fmt.Printf("  Public Key: %s\n", user.PublicKey)
			fmt.Printf("  JWT: %s...\n", user.JWT[:50])

			// Get full credentials
			fullCreds, err := jwtService.GetUserCredentials(ctx, user)
			if err != nil {
				log.Fatalf("Failed to get user credentials: %v", err)
			}
			fmt.Printf("\n=== GENERATED CREDENTIALS (should match encrypted) ===\n")
			fmt.Printf("%s\n", fullCreds)
			fmt.Printf("=======================================================\n")
			break
		}
	}

	// Save credentials to temp file for NATS client
	tmpFile := "/tmp/nats-test-creds.txt"
	err = os.WriteFile(tmpFile, []byte(creds), 0600)
	if err != nil {
		log.Fatalf("Failed to write credentials file: %v", err)
	}
	defer os.Remove(tmpFile)

	// Try to connect to NATS
	fmt.Printf("\n=== ATTEMPTING NATS CONNECTION ===\n")
	for _, serverURL := range cluster.ServerURLs {
		fmt.Printf("Connecting to %s...\n", serverURL)

		opts := []nats.Option{
			nats.UserCredentials(tmpFile),
			nats.Timeout(5 * time.Second),
			nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
				fmt.Printf("NATS Error: %v\n", err)
			}),
			nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
				if err != nil {
					fmt.Printf("NATS Disconnect Error: %v\n", err)
				}
			}),
		}

		nc, err := nats.Connect(serverURL, opts...)
		if err != nil {
			fmt.Printf("❌ Connection failed: %v\n", err)
			continue
		}

		fmt.Printf("✅ Connected successfully!\n")
		fmt.Printf("   Server Info: %+v\n", nc.ConnectedServerName())
		fmt.Printf("   Connected URL: %s\n", nc.ConnectedUrl())

		// Try to get server info
		if nc.Status() == nats.CONNECTED {
			fmt.Printf("   Status: CONNECTED\n")
		}

		nc.Close()
		break
	}
}
