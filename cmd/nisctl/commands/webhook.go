package commands

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhook subscriptions",
}

var webhookCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a webhook subscription",
	RunE:  runWebhookCreate,
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List webhook subscriptions",
	RunE:  runWebhookList,
}

var webhookGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get a webhook subscription",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhookGet,
}

var webhookUpdateCmd = &cobra.Command{
	Use:   "update ID",
	Short: "Update a webhook subscription",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhookUpdate,
}

var webhookDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete a webhook subscription",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhookDelete,
}

var webhookTestCmd = &cobra.Command{
	Use:   "test ID",
	Short: "Send a test event to a webhook subscription",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhookTest,
}

var webhookDeliveriesCmd = &cobra.Command{
	Use:   "deliveries SUBSCRIPTION_ID",
	Short: "List deliveries for a webhook subscription",
	Args:  cobra.ExactArgs(1),
	RunE:  runWebhookDeliveries,
}

var (
	webhookOperator     string
	webhookName         string
	webhookDescription  string
	webhookURL          string
	webhookEventTypes   []string
	webhookEnabled      bool
	webhookForce        bool
	webhookDelStatus    string
)

func init() {
	rootCmd.AddCommand(webhookCmd)
	webhookCmd.AddCommand(webhookCreateCmd)
	webhookCmd.AddCommand(webhookListCmd)
	webhookCmd.AddCommand(webhookGetCmd)
	webhookCmd.AddCommand(webhookUpdateCmd)
	webhookCmd.AddCommand(webhookDeleteCmd)
	webhookCmd.AddCommand(webhookTestCmd)
	webhookCmd.AddCommand(webhookDeliveriesCmd)

	webhookCreateCmd.Flags().StringVar(&webhookOperator, "operator", "", "operator ID or name (required)")
	_ = webhookCreateCmd.MarkFlagRequired("operator")
	webhookCreateCmd.Flags().StringVar(&webhookName, "name", "", "subscription name (required)")
	_ = webhookCreateCmd.MarkFlagRequired("name")
	webhookCreateCmd.Flags().StringVar(&webhookDescription, "description", "", "subscription description")
	webhookCreateCmd.Flags().StringVar(&webhookURL, "url", "", "webhook endpoint URL (required)")
	_ = webhookCreateCmd.MarkFlagRequired("url")
	webhookCreateCmd.Flags().StringSliceVar(&webhookEventTypes, "event-types", nil, "event types to subscribe to (e.g. account.created or '*')")

	webhookListCmd.Flags().StringVar(&webhookOperator, "operator", "", "filter by operator ID or name")

	webhookUpdateCmd.Flags().StringVar(&webhookName, "name", "", "new name")
	webhookUpdateCmd.Flags().StringVar(&webhookDescription, "description", "", "new description")
	webhookUpdateCmd.Flags().StringVar(&webhookURL, "url", "", "new URL")
	webhookUpdateCmd.Flags().StringSliceVar(&webhookEventTypes, "event-types", nil, "replace event type list")
	webhookUpdateCmd.Flags().BoolVar(&webhookEnabled, "enabled", true, "enable or disable the subscription")

	webhookDeleteCmd.Flags().BoolVarP(&webhookForce, "force", "f", false, "skip confirmation prompt")

	webhookDeliveriesCmd.Flags().StringVar(&webhookDelStatus, "status", "", "filter by delivery status (pending|succeeded|failed|dead_letter)")
}

func runWebhookCreate(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	opID, err := resolveOperatorID(webhookOperator)
	if err != nil {
		return err
	}

	resp, err := GetClient().Webhook.CreateWebhookSubscription(context.Background(), connect.NewRequest(&nisv1.CreateWebhookSubscriptionRequest{
		OperatorId:  opID,
		Name:        webhookName,
		Description: webhookDescription,
		Url:         webhookURL,
		EventTypes:  webhookEventTypes,
	}))
	if err != nil {
		return fmt.Errorf("failed to create webhook subscription: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Subscription.Id)
		return nil
	}

	printer.PrintSuccess("Webhook subscription created. Secret (store now — not shown again):\n  %s", resp.Msg.Secret)
	return printer.PrintObject(resp.Msg.Subscription)
}

func runWebhookList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	req := &nisv1.ListWebhookSubscriptionsRequest{}
	if webhookOperator != "" {
		opID, err := resolveOperatorID(webhookOperator)
		if err != nil {
			return err
		}
		req.OperatorId = opID
	}

	resp, err := GetClient().Webhook.ListWebhookSubscriptions(context.Background(), connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to list webhook subscriptions: %w", err)
	}

	if len(resp.Msg.Subscriptions) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No webhook subscriptions found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "URL", "ENABLED", "OPERATOR"}
		rows := make([][]string, len(resp.Msg.Subscriptions))
		for i, s := range resp.Msg.Subscriptions {
			rows[i] = []string{
				client.WebhookID(s.Id),
				s.Name,
				s.Url,
				client.BoolBadge(s.Enabled, "enabled", "disabled"),
				client.OperatorID(s.OperatorId),
			}
		}
		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Subscriptions)
}

func runWebhookGet(cmd *cobra.Command, args []string) error {
	resp, err := GetClient().Webhook.GetWebhookSubscription(context.Background(), connect.NewRequest(&nisv1.GetWebhookSubscriptionRequest{
		Id: args[0],
	}))
	if err != nil {
		return fmt.Errorf("failed to get webhook subscription: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		client.NewPrinter(GetOutputFormat()).PrintID(resp.Msg.Subscription.Id)
		return nil
	}

	return client.NewPrinter(GetOutputFormat()).PrintObject(resp.Msg.Subscription)
}

func runWebhookUpdate(cmd *cobra.Command, args []string) error {
	req := &nisv1.UpdateWebhookSubscriptionRequest{Id: args[0]}

	if cmd.Flags().Changed("name") {
		req.Name = &webhookName
	}
	if cmd.Flags().Changed("description") {
		req.Description = &webhookDescription
	}
	if cmd.Flags().Changed("url") {
		req.Url = &webhookURL
	}
	if cmd.Flags().Changed("event-types") {
		req.EventTypes = webhookEventTypes
	}
	if cmd.Flags().Changed("enabled") {
		req.Enabled = &webhookEnabled
	}

	resp, err := GetClient().Webhook.UpdateWebhookSubscription(context.Background(), connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to update webhook subscription: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		client.NewPrinter(GetOutputFormat()).PrintID(resp.Msg.Subscription.Id)
		return nil
	}

	return client.NewPrinter(GetOutputFormat()).PrintObject(resp.Msg.Subscription)
}

func runWebhookDelete(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	if !webhookForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("webhook subscription", args[0]) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	_, err := GetClient().Webhook.DeleteWebhookSubscription(context.Background(), connect.NewRequest(&nisv1.DeleteWebhookSubscriptionRequest{
		Id: args[0],
	}))
	if err != nil {
		return fmt.Errorf("failed to delete webhook subscription: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Webhook subscription '%s' deleted", args[0])
	}
	return nil
}

func runWebhookTest(cmd *cobra.Command, args []string) error {
	resp, err := GetClient().Webhook.TestWebhookSubscription(context.Background(), connect.NewRequest(&nisv1.TestWebhookSubscriptionRequest{
		Id: args[0],
	}))
	if err != nil {
		return fmt.Errorf("failed to test webhook subscription: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		client.NewPrinter(GetOutputFormat()).PrintID(resp.Msg.DeliveryId)
		return nil
	}

	client.NewPrinter(GetOutputFormat()).PrintSuccess("Test delivery queued: %s", resp.Msg.DeliveryId)
	return nil
}

func runWebhookDeliveries(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Webhook.ListWebhookDeliveries(context.Background(), connect.NewRequest(&nisv1.ListWebhookDeliveriesRequest{
		SubscriptionId: args[0],
		Status:         webhookDelStatus,
	}))
	if err != nil {
		return fmt.Errorf("failed to list webhook deliveries: %w", err)
	}

	if len(resp.Msg.Deliveries) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No deliveries found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "EVENT ID", "ATTEMPT", "STATUS", "RESPONSE CODE"}
		rows := make([][]string, len(resp.Msg.Deliveries))
		for i, d := range resp.Msg.Deliveries {
			rows[i] = []string{
				client.WebhookID(d.Id),
				d.EventId,
				fmt.Sprintf("%d", d.Attempt),
				client.WebhookDeliveryStatusBadge(d.Status),
				fmt.Sprintf("%d", d.LastResponseCode),
			}
		}
		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Deliveries)
}
