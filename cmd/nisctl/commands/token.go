package commands

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage long-lived API tokens for service-account authentication",
}

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API token (plaintext is printed once)",
	RunE:  runTokenCreate,
}

var tokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API tokens visible to the caller",
	RunE:  runTokenList,
}

var tokenGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get details for a single API token",
	Args:  cobra.ExactArgs(1),
	RunE:  runTokenGet,
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke ID",
	Short: "Revoke an API token (keeps the row, blocks further use)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTokenRevoke,
}

var tokenDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Permanently delete an API token row",
	Args:  cobra.ExactArgs(1),
	RunE:  runTokenDelete,
}

var (
	tokenName           string
	tokenDescription    string
	tokenRole           string
	tokenOperator       string
	tokenAccount        string
	tokenOrg            string
	tokenExpiresIn      time.Duration
	tokenIncludeRevoked bool
	tokenForce          bool

	tokenListLimit  int32
	tokenListCursor string
)

func init() {
	rootCmd.AddCommand(tokenCmd)
	tokenCmd.AddCommand(tokenCreateCmd)
	tokenCmd.AddCommand(tokenListCmd)
	tokenCmd.AddCommand(tokenGetCmd)
	tokenCmd.AddCommand(tokenRevokeCmd)
	tokenCmd.AddCommand(tokenDeleteCmd)

	tokenCreateCmd.Flags().StringVar(&tokenName, "name", "", "token name (required, unique per creator)")
	_ = tokenCreateCmd.MarkFlagRequired("name")
	tokenCreateCmd.Flags().StringVar(&tokenDescription, "description", "", "free-form description")
	tokenCreateCmd.Flags().StringVar(&tokenRole, "role", "", "admin | org-admin | operator-admin | account-admin (required)")
	_ = tokenCreateCmd.MarkFlagRequired("role")
	tokenCreateCmd.Flags().StringVar(&tokenOperator, "operator", "", "operator ID or name (required for operator-admin)")
	tokenCreateCmd.Flags().StringVar(&tokenAccount, "account", "", "account ID (required for account-admin)")
	tokenCreateCmd.Flags().StringVar(&tokenOrg, "org", "", "organization ID (admin only; org-admin callers always mint in their own org)")
	tokenCreateCmd.Flags().DurationVar(&tokenExpiresIn, "expires-in", 0, "expiry duration (e.g. 720h, 90d unsupported — use h); zero = never expires")

	tokenListCmd.Flags().BoolVar(&tokenIncludeRevoked, "include-revoked", false, "include revoked tokens in the listing")
	tokenListCmd.Flags().Int32Var(&tokenListLimit, "limit", 0, "fetch only this many rows in one page (default: fetch all)")
	tokenListCmd.Flags().StringVar(&tokenListCursor, "cursor", "", "start from this opaque cursor (single-page mode)")

	tokenDeleteCmd.Flags().BoolVarP(&tokenForce, "force", "f", false, "skip confirmation prompt")
}

func runTokenCreate(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	req := &nisv1.CreateAPITokenRequest{
		Name:        tokenName,
		Description: tokenDescription,
		Role:        tokenRole,
	}
	if tokenOperator != "" {
		opID, err := resolveOperatorID(tokenOperator)
		if err != nil {
			return err
		}
		req.OperatorId = opID
	}
	if tokenAccount != "" {
		req.AccountId = tokenAccount
	}
	if tokenOrg != "" {
		req.OrganizationId = tokenOrg
	}
	if tokenExpiresIn > 0 {
		req.ExpiresAt = timestamppb.New(time.Now().Add(tokenExpiresIn))
	}

	resp, err := GetClient().APIToken.CreateAPIToken(context.Background(), connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to create api token: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		// Print plaintext to stdout so it's pipeable.
		fmt.Println(resp.Msg.Plaintext)
		return nil
	}

	printer.PrintSuccess("API token created. Save the plaintext NOW — it will not be shown again:\n  %s", resp.Msg.Plaintext)
	printer.PrintMessage("Use it by exporting NIS_TOKEN=<value> in CI, or passing --token to nisctl.")
	return printer.PrintObject(resp.Msg.Token)
}

func runTokenList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	singlePage := cmd.Flags().Changed("limit") || cmd.Flags().Changed("cursor")
	var allTokens []*nisv1.APIToken
	cursor := tokenListCursor
	pageNum := 0

	for {
		pageLimit := tokenListLimit
		if !singlePage {
			pageLimit = 200
		}
		resp, err := GetClient().APIToken.ListAPITokens(context.Background(), connect.NewRequest(&nisv1.ListAPITokensRequest{
			IncludeRevoked: tokenIncludeRevoked,
			Page:           &nisv1.PageRequest{Limit: pageLimit, Cursor: cursor},
		}))
		if err != nil {
			return fmt.Errorf("failed to list api tokens: %w", err)
		}
		allTokens = append(allTokens, resp.Msg.Tokens...)
		pageNum++
		if singlePage {
			if resp.Msg.NextCursor != "" && GetOutputFormat() == "table" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "next-cursor: %s\n", resp.Msg.NextCursor)
			}
			break
		}
		if resp.Msg.NextCursor == "" {
			break
		}
		if pageNum > 1 && GetOutputFormat() == "table" {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "fetching page %d...\n", pageNum+1)
		}
		cursor = resp.Msg.NextCursor
	}

	if len(allTokens) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No API tokens found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "ROLE", "PREFIX", "LAST USED", "STATUS"}
		rows := make([][]string, len(allTokens))
		for i, t := range allTokens {
			lastUsed := "—"
			if t.LastUsedAt != nil {
				lastUsed = t.LastUsedAt.AsTime().Local().Format(time.RFC3339)
			}
			revoked := t.RevokedAt != nil
			expired := !revoked && t.ExpiresAt != nil && t.ExpiresAt.AsTime().Before(time.Now())
			rows[i] = []string{
				client.APITokenID(t.Id),
				t.Name,
				t.Role,
				t.Prefix,
				lastUsed,
				client.APITokenStatusBadge(revoked, expired),
			}
		}
		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(allTokens)
}

func runTokenGet(cmd *cobra.Command, args []string) error {
	resp, err := GetClient().APIToken.GetAPIToken(context.Background(), connect.NewRequest(&nisv1.GetAPITokenRequest{
		Id: args[0],
	}))
	if err != nil {
		return fmt.Errorf("failed to get api token: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		client.NewPrinter(GetOutputFormat()).PrintID(resp.Msg.Token.Id)
		return nil
	}
	return client.NewPrinter(GetOutputFormat()).PrintObject(resp.Msg.Token)
}

func runTokenRevoke(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().APIToken.RevokeAPIToken(context.Background(), connect.NewRequest(&nisv1.RevokeAPITokenRequest{
		Id: args[0],
	}))
	if err != nil {
		return fmt.Errorf("failed to revoke api token: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Token.Id)
		return nil
	}

	printer.PrintSuccess("API token '%s' revoked", args[0])
	return printer.PrintObject(resp.Msg.Token)
}

func runTokenDelete(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	if !tokenForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("api token", args[0]) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	_, err := GetClient().APIToken.DeleteAPIToken(context.Background(), connect.NewRequest(&nisv1.DeleteAPITokenRequest{
		Id: args[0],
	}))
	if err != nil {
		return fmt.Errorf("failed to delete api token: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("API token '%s' deleted", args[0])
	}
	return nil
}
