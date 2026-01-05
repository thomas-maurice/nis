package commands

import (
	"context"
	"fmt"

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

var (
	clusterOperatorID  string
	clusterURLs        []string
	clusterDescription string
	clusterForce       bool
)

func init() {
	rootCmd.AddCommand(clusterCmd)

	clusterCmd.AddCommand(clusterCreateCmd)
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)
	clusterCmd.AddCommand(clusterDeleteCmd)
	clusterCmd.AddCommand(clusterSyncCmd)

	clusterCreateCmd.Flags().StringVar(&clusterOperatorID, "operator", "", "operator ID or name (required)")
	clusterCreateCmd.Flags().StringSliceVar(&clusterURLs, "urls", []string{}, "NATS server URLs (required)")
	clusterCreateCmd.Flags().StringVar(&clusterDescription, "description", "", "cluster description")
	clusterCreateCmd.MarkFlagRequired("operator")
	clusterCreateCmd.MarkFlagRequired("urls")

	clusterDeleteCmd.Flags().BoolVarP(&clusterForce, "force", "f", false, "skip confirmation prompt")
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
		printer.PrintMessage("Syncing accounts to cluster...")
	}

	// Call sync API
	req := connect.NewRequest(&nisv1.SyncClusterRequest{
		Id: clusterID,
	})

	resp, err := GetClient().Cluster.SyncCluster(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to sync cluster: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Successfully synced %d accounts to cluster", resp.Msg.AccountCount)
		for _, account := range resp.Msg.Accounts {
			printer.PrintMessage("  - %s", account)
		}
	}

	return nil
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
