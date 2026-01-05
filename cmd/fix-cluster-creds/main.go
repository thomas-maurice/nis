package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
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
	scopedKeyRepo := sql.NewScopedSigningKeyRepo(db)

	// Create JWT service
	jwtService := services.NewJWTService(encryptor)

	// Create cluster service
	clusterService := services.NewClusterService(
		clusterRepo,
		operatorRepo,
		accountRepo,
		userRepo,
		scopedKeyRepo,
		encryptor,
		jwtService,
	)

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
			fmt.Printf("  Operator ID: %s\n", cluster.OperatorID)
			fmt.Printf("  Encrypted creds length: %d\n", len(cluster.EncryptedCreds))
			break
		}
	}

	if lilCluster == nil {
		log.Fatal("lil cluster not found")
	}

	// Get the operator
	cluster, _ := clusterRepo.GetByID(ctx, *lilCluster)
	operator, err := operatorRepo.GetByID(ctx, cluster.OperatorID)
	if err != nil {
		log.Fatalf("Failed to get operator: %v", err)
	}

	fmt.Printf("Operator: %s (ID: %s)\n", operator.Name, operator.ID)
	fmt.Printf("System Account Pub Key: %s\n", operator.SystemAccountPubKey)

	// Find system account
	accounts, err := accountRepo.ListByOperator(ctx, operator.ID, repositories.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list accounts: %v", err)
	}

	var sysAccount *uuid.UUID
	for _, account := range accounts {
		fmt.Printf("Account: %s (ID: %s, PubKey: %s)\n", account.Name, account.ID, account.PublicKey)
		if account.PublicKey == operator.SystemAccountPubKey {
			sysAccount = &account.ID
			fmt.Printf("  ^^ This is the system account\n")
		}
	}

	if sysAccount == nil {
		log.Fatal("System account not found")
	}

	// Find system user
	users, err := userRepo.ListByAccount(ctx, *sysAccount, repositories.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list users: %v", err)
	}

	var systemUser *uuid.UUID
	for _, user := range users {
		fmt.Printf("User: %s (ID: %s)\n", user.Name, user.ID)
		if user.Name == "system" {
			systemUser = &user.ID
			fmt.Printf("  ^^ This is the system user\n")
		}
	}

	if systemUser == nil {
		log.Fatal("System user not found")
	}

	// Update cluster credentials
	fmt.Printf("\nUpdating cluster credentials...\n")
	updatedCluster, err := clusterService.UpdateClusterCredentials(ctx, *lilCluster, *systemUser)
	if err != nil {
		log.Fatalf("Failed to update cluster credentials: %v", err)
	}

	fmt.Printf("Success! Encrypted creds length: %d\n", len(updatedCluster.EncryptedCreds))

	// Verify in database
	verifyCluster, err := clusterRepo.GetByID(ctx, *lilCluster)
	if err != nil {
		log.Fatalf("Failed to verify cluster: %v", err)
	}

	fmt.Printf("Verified in DB - Encrypted creds length: %d\n", len(verifyCluster.EncryptedCreds))
}
