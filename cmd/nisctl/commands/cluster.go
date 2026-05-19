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

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage NATS clusters",
	Long:  `Create, list, update, and delete NATS clusters.`,
}

var clusterCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new cluster",
	Args:  cobra.ExactArgs(1),
	RunE:  runClusterCreate,
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all clusters",
	RunE:  runClusterList,
}

var clusterGetCmd = &cobra.Command{
	Use:   "get ID_OR_NAME",
	Short: "Get cluster details",
	Args:  cobra.ExactArgs(1),
	RunE:  runClusterGet,
}

var clusterDeleteCmd = &cobra.Command{
	Use:   "delete ID_OR_NAME",
	Short: "Delete a cluster",
	Args:  cobra.ExactArgs(1),
	RunE:  runClusterDelete,
}

var clusterSyncCmd = &cobra.Command{
	Use:   "sync ID_OR_NAME",
	Short: "Sync all accounts to the cluster",
	Long:  `Push all account JWTs for the operator to the NATS cluster resolver.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runClusterSync,
}

var clusterResolverAccountsCmd = &cobra.Command{
	Use:   "resolver-accounts ID_OR_NAME",
	Short: "List accounts on the NATS resolver",
	Long:  `List all account public keys currently stored in the NATS resolver.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runClusterResolverAccounts,
}

var clusterDeleteResolverAccountCmd = &cobra.Command{
	Use:   "delete-resolver-account ID_OR_NAME PUBLIC_KEY",
	Short: "Delete an account from the NATS resolver",
	Long:  `Remove an account JWT from the NATS resolver. This does not delete the account from the database.`,
	Args:  cobra.ExactArgs(2),
	RunE:  runClusterDeleteResolverAccount,
}

var clusterDriftCmd = &cobra.Command{
	Use:   "drift ID_OR_NAME",
	Short: "Compare cluster resolver JWTs against the NIS database",
	Long: `Scan every account on the operator and report whether each account's
NIS-DB JWT matches what the NATS full-resolver currently has stored.

Status values:
  in_sync             — resolver agrees with NIS DB
  db_ahead            — NIS regenerated, push needed (use 'reconcile-account')
  out_of_band         — resolver has a JWT NIS does not recognise
  missing_on_resolver — NIS has it, resolver does not
  unreachable         — could not probe (dial / decrypt / resolver error)`,
	Args: cobra.ExactArgs(1),
	RunE: runClusterDrift,
}

var clusterReconcileAccountCmd = &cobra.Command{
	Use:   "reconcile-account CLUSTER_ID_OR_NAME",
	Short: "Push a single account's JWT to one cluster",
	Long: `Push the NIS-DB JWT for a single account to the specified cluster's NATS
resolver. Intended for "fix this drifted row" — much cheaper than 'cluster sync'
when only one account is out of date.`,
	Args: cobra.ExactArgs(1),
	RunE: runClusterReconcileAccount,
}

var (
	clusterOperatorID        string
	clusterURLs              []string
	clusterDescription       string
	clusterForce             bool
	clusterSyncPrune         bool
	clusterDeleteForce       bool
	clusterDriftIncludeInSync bool
	clusterReconcileOperator string
	clusterReconcileAccount  string
)

func init() {
	rootCmd.AddCommand(clusterCmd)

	clusterCmd.AddCommand(clusterCreateCmd)
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)
	clusterCmd.AddCommand(clusterDeleteCmd)
	clusterCmd.AddCommand(clusterSyncCmd)
	clusterCmd.AddCommand(clusterResolverAccountsCmd)
	clusterCmd.AddCommand(clusterDeleteResolverAccountCmd)
	clusterCmd.AddCommand(clusterDriftCmd)
	clusterCmd.AddCommand(clusterReconcileAccountCmd)

	clusterCreateCmd.Flags().StringVar(&clusterOperatorID, "operator", "", "operator ID or name (required)")
	clusterCreateCmd.Flags().StringSliceVar(&clusterURLs, "urls", []string{}, "NATS server URLs (required)")
	clusterCreateCmd.Flags().StringVar(&clusterDescription, "description", "", "cluster description")
	_ = clusterCreateCmd.MarkFlagRequired("operator")
	_ = clusterCreateCmd.MarkFlagRequired("urls")

	clusterDeleteCmd.Flags().BoolVarP(&clusterForce, "force", "f", false, "skip confirmation prompt")

	clusterSyncCmd.Flags().BoolVar(&clusterSyncPrune, "prune", false, "remove accounts from resolver that are not in the database")

	clusterDeleteResolverAccountCmd.Flags().BoolVarP(&clusterDeleteForce, "force", "f", false, "skip confirmation prompt")

	clusterDriftCmd.Flags().BoolVar(&clusterDriftIncludeInSync, "include-in-sync", false, "include accounts whose resolver JWT matches NIS")

	clusterReconcileAccountCmd.Flags().StringVar(&clusterReconcileOperator, "operator", "", "operator ID or name (required)")
	clusterReconcileAccountCmd.Flags().StringVar(&clusterReconcileAccount, "account", "", "account name to reconcile (required)")
	_ = clusterReconcileAccountCmd.MarkFlagRequired("operator")
	_ = clusterReconcileAccountCmd.MarkFlagRequired("account")
}

func runClusterCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(clusterOperatorID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&nisv1.CreateClusterRequest{
		OperatorId:  operatorID,
		Name:        name,
		Description: clusterDescription,
		ServerUrls:  clusterURLs,
	})

	resp, err := GetClient().Cluster.CreateCluster(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Cluster.Id)
		return nil
	}

	printer.PrintSuccess("Cluster created successfully")
	printer.PrintMessage("System user automatically created for cluster management")
	return printer.PrintObject(resp.Msg.Cluster)
}

func runClusterList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.ListClustersRequest{})

	resp, err := GetClient().Cluster.ListClusters(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	if len(resp.Msg.Clusters) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No clusters found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "DESCRIPTION", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Clusters))

		for i, cluster := range resp.Msg.Clusters {
			description := cluster.Description
			if description == "" {
				description = "-"
			}

			createdAt := "-"
			if cluster.CreatedAt != nil {
				createdAt = cluster.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				cluster.Id[:8] + "...",
				cluster.Name,
				description,
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Clusters)
}

func runClusterGet(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.GetClusterRequest{
		Id: idOrName,
	})

	resp, err := GetClient().Cluster.GetCluster(context.Background(), req)
	if err != nil {
		nameReq := connect.NewRequest(&nisv1.GetClusterByNameRequest{
			Name: idOrName,
		})

		nameResp, nameErr := GetClient().Cluster.GetClusterByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("cluster not found: %w", nameErr)
		}

		resp = &connect.Response[nisv1.GetClusterResponse]{
			Msg: &nisv1.GetClusterResponse{
				Cluster: nameResp.Msg.Cluster,
			},
		}
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Cluster.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.Cluster)
}

func runClusterDelete(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	var clusterID, clusterName string

	getReq := connect.NewRequest(&nisv1.GetClusterRequest{
		Id: idOrName,
	})

	getResp, err := GetClient().Cluster.GetCluster(context.Background(), getReq)
	if err != nil {
		nameReq := connect.NewRequest(&nisv1.GetClusterByNameRequest{
			Name: idOrName,
		})

		nameResp, nameErr := GetClient().Cluster.GetClusterByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("cluster not found: %w", nameErr)
		}

		clusterID = nameResp.Msg.Cluster.Id
		clusterName = nameResp.Msg.Cluster.Name
	} else {
		clusterID = getResp.Msg.Cluster.Id
		clusterName = getResp.Msg.Cluster.Name
	}

	if !clusterForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("cluster", clusterName) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	req := connect.NewRequest(&nisv1.DeleteClusterRequest{
		Id: clusterID,
	})

	_, err = GetClient().Cluster.DeleteCluster(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Cluster '%s' deleted successfully", clusterName)
	}

	return nil
}

func runClusterSync(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve cluster ID
	clusterID, err := resolveClusterID(idOrName)
	if err != nil {
		return err
	}

	if GetOutputFormat() != "quiet" {
		if clusterSyncPrune {
			printer.PrintMessage("Syncing accounts to cluster (with pruning)...")
		} else {
			printer.PrintMessage("Syncing accounts to cluster...")
		}
	}

	// Call sync API
	req := connect.NewRequest(&nisv1.SyncClusterRequest{
		Id:    clusterID,
		Prune: clusterSyncPrune,
	})

	resp, err := GetClient().Cluster.SyncCluster(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to sync cluster: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Successfully synced %d accounts to cluster", resp.Msg.AccountCount)
		if len(resp.Msg.Accounts) > 0 {
			printer.PrintMessage("Updated accounts:")
			for _, account := range resp.Msg.Accounts {
				printer.PrintMessage("  - %s", account)
			}
		}
		if len(resp.Msg.RemovedAccounts) > 0 {
			printer.PrintMessage("Removed accounts:")
			for _, pubKey := range resp.Msg.RemovedAccounts {
				printer.PrintMessage("  - %s", pubKey)
			}
		}
		if len(resp.Msg.Errors) > 0 {
			printer.PrintMessage("Errors encountered:")
			for _, syncErr := range resp.Msg.Errors {
				if syncErr.AccountName != "" {
					printer.PrintMessage("  - %s (%s): %s", syncErr.AccountName, syncErr.AccountPublicKey, syncErr.Error)
				} else {
					printer.PrintMessage("  - %s", syncErr.Error)
				}
			}
		}
	}

	return nil
}

func runClusterResolverAccounts(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve cluster ID
	clusterID, err := resolveClusterID(idOrName)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&nisv1.ListResolverAccountsRequest{
		ClusterId: clusterID,
	})

	resp, err := GetClient().Cluster.ListResolverAccounts(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list resolver accounts: %w", err)
	}

	if len(resp.Msg.PublicKeys) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No accounts found on resolver")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"PUBLIC KEY"}
		rows := make([][]string, len(resp.Msg.PublicKeys))
		for i, pubKey := range resp.Msg.PublicKeys {
			rows[i] = []string{pubKey}
		}
		return printer.PrintTable(headers, rows)
	}

	if GetOutputFormat() == "quiet" {
		for _, pubKey := range resp.Msg.PublicKeys {
			fmt.Println(pubKey)
		}
		return nil
	}

	return printer.PrintList(resp.Msg.PublicKeys)
}

func runClusterDeleteResolverAccount(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	publicKey := args[1]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve cluster ID
	clusterID, err := resolveClusterID(idOrName)
	if err != nil {
		return err
	}

	if !clusterDeleteForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("resolver account", publicKey) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	req := connect.NewRequest(&nisv1.DeleteResolverAccountRequest{
		ClusterId: clusterID,
		PublicKey: publicKey,
	})

	_, err = GetClient().Cluster.DeleteResolverAccount(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete resolver account: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Account '%s' deleted from resolver", publicKey)
	}

	return nil
}

func runClusterDrift(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	clusterID, err := resolveClusterID(idOrName)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     clusterID,
		IncludeInSync: clusterDriftIncludeInSync,
	})
	resp, err := GetClient().Cluster.GetClusterDriftStatus(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to scan cluster drift: %w", err)
	}

	if len(resp.Msg.Rows) == 0 {
		if GetOutputFormat() != "quiet" {
			if clusterDriftIncludeInSync {
				printer.PrintMessage("No accounts found on this operator")
			} else {
				printer.PrintMessage("No drifted accounts — every account is in_sync")
			}
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ACCOUNT", "STATUS", "NIS IAT", "RESOLVER IAT", "ERROR"}
		rows := make([][]string, len(resp.Msg.Rows))
		for i, r := range resp.Msg.Rows {
			rows[i] = []string{
				r.AccountName,
				driftStatusLabelFromProto(r.Status),
				formatIATAsLocal(r.NisJwtIat),
				formatIATAsLocal(r.ResolverJwtIat),
				truncate(r.ErrorMessage, 60),
			}
		}
		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Rows)
}

func runClusterReconcileAccount(cmd *cobra.Command, args []string) error {
	idOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	clusterID, err := resolveClusterID(idOrName)
	if err != nil {
		return err
	}

	operatorID, err := resolveOperatorID(clusterReconcileOperator)
	if err != nil {
		return err
	}

	// Resolve account by name within the operator.
	accReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       clusterReconcileAccount,
	})
	accResp, err := GetClient().Account.GetAccountByName(context.Background(), accReq)
	if err != nil {
		return fmt.Errorf("account %q not found in operator %q: %w", clusterReconcileAccount, clusterReconcileOperator, err)
	}

	req := connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: clusterID,
		AccountId: accResp.Msg.Account.Id,
	})
	if _, err := GetClient().Cluster.ReconcileAccountOnCluster(context.Background(), req); err != nil {
		return fmt.Errorf("failed to reconcile account: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Pushed account %q to cluster", clusterReconcileAccount)
	}
	return nil
}

// driftStatusLabelFromProto mirrors the lowercase label set used in the
// metrics counter so CLI output and Prometheus labels agree.
func driftStatusLabelFromProto(s nisv1.DriftStatus) string {
	switch s {
	case nisv1.DriftStatus_DRIFT_STATUS_IN_SYNC:
		return "in_sync"
	case nisv1.DriftStatus_DRIFT_STATUS_DB_AHEAD:
		return "db_ahead"
	case nisv1.DriftStatus_DRIFT_STATUS_OUT_OF_BAND:
		return "out_of_band"
	case nisv1.DriftStatus_DRIFT_STATUS_MISSING_ON_RESOLVER:
		return "missing_on_resolver"
	case nisv1.DriftStatus_DRIFT_STATUS_UNREACHABLE:
		return "unreachable"
	}
	return "unspecified"
}

// formatIATAsLocal renders a Unix-seconds JWT IssuedAt in the viewer's local
// timezone, matching the codebase rule (SKILL §2 "Time discipline"): storage
// is UTC, display converts at the edge.
func formatIATAsLocal(iat int64) string {
	if iat == 0 {
		return "-"
	}
	return time.Unix(iat, 0).Local().Format("2006-01-02 15:04:05")
}

func resolveClusterID(idOrName string) (string, error) {
	req := connect.NewRequest(&nisv1.GetClusterRequest{
		Id: idOrName,
	})

	resp, err := GetClient().Cluster.GetCluster(context.Background(), req)
	if err == nil {
		return resp.Msg.Cluster.Id, nil
	}

	nameReq := connect.NewRequest(&nisv1.GetClusterByNameRequest{
		Name: idOrName,
	})

	nameResp, err := GetClient().Cluster.GetClusterByName(context.Background(), nameReq)
	if err != nil {
		return "", fmt.Errorf("cluster not found: %w", err)
	}

	return nameResp.Msg.Cluster.Id, nil
}
