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

	// Get the operator and system account
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

	fmt.Printf("\nFound %d users in SYS account:\n", len(users))
	for i, user := range users {
		fmt.Printf("%d. %s (created: %s)\n", i+1, user.Name, user.CreatedAt.Format(time.RFC3339))
	}

	// Try each user
	for _, user := range users {
		fmt.Printf("\n=== Testing user: %s ===\n", user.Name)

		// Get full credentials
		fullCreds, err := jwtService.GetUserCredentials(ctx, user)
		if err != nil {
			log.Printf("Failed to get credentials for %s: %v", user.Name, err)
			continue
		}

		// Save credentials to temp file
		tmpFile := fmt.Sprintf("/tmp/nats-test-%s.txt", user.Name)
		err = os.WriteFile(tmpFile, []byte(fullCreds), 0600)
		if err != nil {
			log.Printf("Failed to write credentials file: %v", err)
			continue
		}
		defer os.Remove(tmpFile)

		// Try to connect
		for _, serverURL := range cluster.ServerURLs {
			fmt.Printf("Connecting to %s with user %s...\n", serverURL, user.Name)

			opts := []nats.Option{
				nats.UserCredentials(tmpFile),
				nats.Timeout(5 * time.Second),
			}

			nc, err := nats.Connect(serverURL, opts...)
			if err != nil {
				fmt.Printf("  ❌ Connection failed: %v\n", err)
				continue
			}

			fmt.Printf("  ✅ Connected successfully!\n")
			fmt.Printf("     Server: %s\n", nc.ConnectedServerName())
			fmt.Printf("     Status: %v\n", nc.Status())

			// Try to list servers
			fmt.Printf("     Attempting to get server info...\n")
			servers := nc.Servers()
			fmt.Printf("     Known servers: %v\n", servers)

			nc.Close()
			fmt.Printf("\n  SUCCESS! User '%s' can connect!\n\n", user.Name)
			return
		}
	}

	fmt.Printf("\n❌ None of the users could connect\n")
}
