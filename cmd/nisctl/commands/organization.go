package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

// ─── org ──────────────────────────────────────────────────────────────────────

var orgCmd = &cobra.Command{
	Use:   "org",
	Short: "Manage organizations",
}

// ─── org create ───────────────────────────────────────────────────────────────

var orgCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new organization",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgCreate,
}

var (
	orgCreateSlug        string
	orgCreateDescription string
)

func runOrgCreate(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Organization.CreateOrganization(context.Background(),
		connect.NewRequest(&nisv1.CreateOrganizationRequest{
			Name:        args[0],
			Slug:        orgCreateSlug,
			Description: orgCreateDescription,
		}))
	if err != nil {
		return fmt.Errorf("create organization: %w", err)
	}
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Organization.Id)
		return nil
	}
	printer.PrintSuccess("Organization created")
	return printer.PrintObject(resp.Msg.Organization)
}

// ─── org list ─────────────────────────────────────────────────────────────────

var orgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List organizations",
	Args:  cobra.NoArgs,
	RunE:  runOrgList,
}

func runOrgList(_ *cobra.Command, _ []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Organization.ListOrganizations(context.Background(),
		connect.NewRequest(&nisv1.ListOrganizationsRequest{}))
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	orgs := resp.Msg.Organizations
	if len(orgs) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No organizations found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "SLUG", "CREATED AT"}
		rows := make([][]string, len(orgs))
		for i, o := range orgs {
			createdAt := "-"
			if o.CreatedAt != nil {
				createdAt = o.CreatedAt.AsTime().Local().Format("2006-01-02 15:04:05")
			}
			rows[i] = []string{o.Id, o.Name, o.Slug, createdAt}
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(orgs)
}

// ─── org get ──────────────────────────────────────────────────────────────────

var orgGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get an organization by ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgGet,
}

var orgGetSlug string

func runOrgGet(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	// Try slug if flag set; otherwise treat positional as ID.
	if orgGetSlug != "" {
		resp, err := GetClient().Organization.GetOrganizationBySlug(context.Background(),
			connect.NewRequest(&nisv1.GetOrganizationBySlugRequest{Slug: orgGetSlug}))
		if err != nil {
			return fmt.Errorf("get organization by slug: %w", err)
		}
		if GetOutputFormat() == "quiet" {
			printer.PrintID(resp.Msg.Organization.Id)
			return nil
		}
		return printer.PrintObject(resp.Msg.Organization)
	}

	resp, err := GetClient().Organization.GetOrganization(context.Background(),
		connect.NewRequest(&nisv1.GetOrganizationRequest{Id: args[0]}))
	if err != nil {
		// Fall back to slug lookup.
		slugResp, slugErr := GetClient().Organization.GetOrganizationBySlug(context.Background(),
			connect.NewRequest(&nisv1.GetOrganizationBySlugRequest{Slug: args[0]}))
		if slugErr != nil {
			return fmt.Errorf("organization not found: %w", err)
		}
		if GetOutputFormat() == "quiet" {
			printer.PrintID(slugResp.Msg.Organization.Id)
			return nil
		}
		return printer.PrintObject(slugResp.Msg.Organization)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Organization.Id)
		return nil
	}
	return printer.PrintObject(resp.Msg.Organization)
}

// ─── org update ───────────────────────────────────────────────────────────────

var orgUpdateCmd = &cobra.Command{
	Use:   "update ID",
	Short: "Update an organization's name or description (slug is immutable)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgUpdate,
}

var (
	orgUpdateName        string
	orgUpdateDescription string
)

func runOrgUpdate(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Organization.UpdateOrganization(context.Background(),
		connect.NewRequest(&nisv1.UpdateOrganizationRequest{
			Id:          args[0],
			Name:        orgUpdateName,
			Description: orgUpdateDescription,
		}))
	if err != nil {
		return fmt.Errorf("update organization: %w", err)
	}
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Organization.Id)
		return nil
	}
	printer.PrintSuccess("Organization updated")
	return printer.PrintObject(resp.Msg.Organization)
}

// ─── org delete ───────────────────────────────────────────────────────────────

var orgDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete an organization",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgDelete,
}

var orgDeleteForce bool

func runOrgDelete(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	if !orgDeleteForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("organization", args[0]) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	_, err := GetClient().Organization.DeleteOrganization(context.Background(),
		connect.NewRequest(&nisv1.DeleteOrganizationRequest{Id: args[0]}))
	if err != nil {
		return fmt.Errorf("delete organization: %w", err)
	}
	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Organization %s deleted", args[0])
	}
	return nil
}

// ─── org sso ──────────────────────────────────────────────────────────────────

var orgSSOCmd = &cobra.Command{
	Use:   "sso",
	Short: "Manage SSO configuration for an organization",
}

// org sso get

var orgSSOGetCmd = &cobra.Command{
	Use:   "get ORG_ID",
	Short: "Get the SSO configuration for an organization",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgSSOGet,
}

func runOrgSSOGet(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Organization.GetSSOConfig(context.Background(),
		connect.NewRequest(&nisv1.GetSSOConfigRequest{OrganizationId: args[0]}))
	if err != nil {
		return fmt.Errorf("get SSO config: %w", err)
	}
	return printer.PrintObject(resp.Msg.Config)
}

// org sso set

var orgSSOSetCmd = &cobra.Command{
	Use:   "set ORG_ID",
	Short: "Create or update the SSO configuration for an organization",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgSSOSet,
}

var (
	orgSSOSetIssuer      string
	orgSSOSetClientID    string
	orgSSOSetClientSecret string
	orgSSOSetEnabled     bool
	orgSSOSetScopes      string
	orgSSOSetGroupClaim  string
	orgSSOSetDefaultRole string
)

func runOrgSSOSet(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Organization.SetSSOConfig(context.Background(),
		connect.NewRequest(&nisv1.SetSSOConfigRequest{
			OrganizationId: args[0],
			Enabled:        orgSSOSetEnabled,
			IssuerUrl:      orgSSOSetIssuer,
			ClientId:       orgSSOSetClientID,
			ClientSecret:   orgSSOSetClientSecret,
			Scopes:         orgSSOSetScopes,
			GroupClaim:     orgSSOSetGroupClaim,
			DefaultRole:    orgSSOSetDefaultRole,
		}))
	if err != nil {
		return fmt.Errorf("set SSO config: %w", err)
	}
	printer.PrintSuccess("SSO config updated")
	return printer.PrintObject(resp.Msg.Config)
}

// org sso delete

var orgSSODeleteCmd = &cobra.Command{
	Use:   "delete ORG_ID",
	Short: "Remove the SSO configuration for an organization",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgSSODelete,
}

func runOrgSSODelete(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	_, err := GetClient().Organization.DeleteSSOConfig(context.Background(),
		connect.NewRequest(&nisv1.DeleteSSOConfigRequest{OrganizationId: args[0]}))
	if err != nil {
		return fmt.Errorf("delete SSO config: %w", err)
	}
	printer.PrintSuccess("SSO config deleted for organization %s", args[0])
	return nil
}

// ─── org sso mappings ─────────────────────────────────────────────────────────

var orgSSOMappingsCmd = &cobra.Command{
	Use:   "mappings",
	Short: "Manage SSO role mappings for an organization",
}

// org sso mappings list

var orgSSOMappingsListCmd = &cobra.Command{
	Use:   "list ORG_ID",
	Short: "List SSO role mappings for an organization",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgSSOMappingsList,
}

func runOrgSSOMappingsList(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Organization.ListSSORoleMappings(context.Background(),
		connect.NewRequest(&nisv1.ListSSORoleMappingsRequest{OrganizationId: args[0]}))
	if err != nil {
		return fmt.Errorf("list SSO mappings: %w", err)
	}

	mappings := resp.Msg.Mappings
	if len(mappings) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No SSO role mappings found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "GROUP", "ROLE", "SCOPE_OPERATOR", "SCOPE_ACCOUNT", "PRIORITY"}
		rows := make([][]string, len(mappings))
		for i, m := range mappings {
			rows[i] = []string{m.Id, m.GroupValue, m.Role, m.ScopeOperatorId, m.ScopeAccountId, fmt.Sprintf("%d", m.Priority)}
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(mappings)
}

// org sso mappings set
//
// Accepts a JSON file (or stdin with "-") containing an array of mapping objects:
//
//	[{"group_value":"admins","role":"org-admin","priority":10}, ...]
//
// Or repeatable --mapping flags: group_value:role:priority (optional :operator_id or :account_id
// appended). For multi-mapping use cases the JSON file approach is simpler.

var orgSSOMappingsSetCmd = &cobra.Command{
	Use: "set ORG_ID",
	Short: "Replace all SSO role mappings for an organization (replaces existing set atomically)",
	Long: `Replace all SSO role mappings for an organization atomically.

Two input modes (choose one):

  --file FILE   Read a JSON array of mapping objects from FILE ("-" for stdin).
                Each object must have: group_value, role, priority.
                Optional: scope_operator_id, scope_account_id.

  --mapping SPEC  Repeatable flag. Format: group_value:role:priority
                  Optionally append @operator_id or @account_id for scoped roles.
                  Example: "devs:operator-admin:20@<operator-uuid>"

Role must be one of: org-admin, operator-admin, account-admin.`,
	Args: cobra.ExactArgs(1),
	RunE: runOrgSSOMappingsSet,
}

var (
	orgSSOMappingsSetFile     string
	orgSSOMappingsSetMappings []string
)

type jsonMappingInput struct {
	GroupValue      string `json:"group_value"`
	Role            string `json:"role"`
	ScopeOperatorID string `json:"scope_operator_id,omitempty"`
	ScopeAccountID  string `json:"scope_account_id,omitempty"`
	Priority        int32  `json:"priority"`
}

func runOrgSSOMappingsSet(_ *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	var inputs []*nisv1.SSORoleMappingInput

	switch {
	case orgSSOMappingsSetFile != "":
		var r []jsonMappingInput
		var data []byte
		var err error
		if orgSSOMappingsSetFile == "-" {
			data, err = os.ReadFile("/dev/stdin")
		} else {
			data, err = os.ReadFile(orgSSOMappingsSetFile)
		}
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("parse JSON: %w", err)
		}
		for _, m := range r {
			inputs = append(inputs, &nisv1.SSORoleMappingInput{
				GroupValue:      m.GroupValue,
				Role:            m.Role,
				ScopeOperatorId: m.ScopeOperatorID,
				ScopeAccountId:  m.ScopeAccountID,
				Priority:        m.Priority,
			})
		}

	case len(orgSSOMappingsSetMappings) > 0:
		for _, spec := range orgSSOMappingsSetMappings {
			inp, err := parseMappingSpec(spec)
			if err != nil {
				return fmt.Errorf("invalid --mapping %q: %w", spec, err)
			}
			inputs = append(inputs, inp)
		}

	default:
		return fmt.Errorf("provide either --file or at least one --mapping flag")
	}

	resp, err := GetClient().Organization.SetSSORoleMappings(context.Background(),
		connect.NewRequest(&nisv1.SetSSORoleMappingsRequest{
			OrganizationId: args[0],
			Mappings:       inputs,
		}))
	if err != nil {
		return fmt.Errorf("set SSO mappings: %w", err)
	}

	printer.PrintSuccess("SSO role mappings updated (%d mappings)", len(resp.Msg.Mappings))
	return printer.PrintList(resp.Msg.Mappings)
}

// parseMappingSpec parses "group:role:priority[@scope_id]".
// The scope_id is interpreted as operator_id for operator-admin and account_id for account-admin.
func parseMappingSpec(spec string) (*nisv1.SSORoleMappingInput, error) {
	// Check for @scope suffix first.
	scopeID := ""
	if idx := strings.LastIndex(spec, "@"); idx >= 0 {
		scopeID = spec[idx+1:]
		spec = spec[:idx]
	}

	parts := strings.Split(spec, ":")
	if len(parts) < 3 {
		return nil, fmt.Errorf("expected format group_value:role:priority (got %d parts)", len(parts))
	}
	groupValue := parts[0]
	role := parts[1]
	// Allow priority part to be empty (defaults to 0).
	var priority int32
	if parts[2] != "" {
		if _, err := fmt.Sscanf(parts[2], "%d", &priority); err != nil {
			return nil, fmt.Errorf("priority must be an integer")
		}
	}

	inp := &nisv1.SSORoleMappingInput{
		GroupValue: groupValue,
		Role:       role,
		Priority:   priority,
	}
	switch role {
	case "operator-admin":
		if scopeID == "" {
			return nil, fmt.Errorf("operator-admin requires @operator_id suffix")
		}
		inp.ScopeOperatorId = scopeID
	case "account-admin":
		if scopeID == "" {
			return nil, fmt.Errorf("account-admin requires @account_id suffix")
		}
		inp.ScopeAccountId = scopeID
	}
	return inp, nil
}

// ─── init ─────────────────────────────────────────────────────────────────────

func init() {
	rootCmd.AddCommand(orgCmd)

	// org subcommands
	orgCmd.AddCommand(orgCreateCmd)
	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgGetCmd)
	orgCmd.AddCommand(orgUpdateCmd)
	orgCmd.AddCommand(orgDeleteCmd)
	orgCmd.AddCommand(orgSSOCmd)

	// org create flags
	orgCreateCmd.Flags().StringVar(&orgCreateSlug, "slug", "", "URL-safe slug [a-z0-9-] (required)")
	_ = orgCreateCmd.MarkFlagRequired("slug")
	orgCreateCmd.Flags().StringVar(&orgCreateDescription, "description", "", "organization description")

	// org get flags
	orgGetCmd.Flags().StringVar(&orgGetSlug, "slug", "", "get by slug instead of ID")

	// org update flags
	orgUpdateCmd.Flags().StringVar(&orgUpdateName, "name", "", "new name")
	orgUpdateCmd.Flags().StringVar(&orgUpdateDescription, "description", "", "new description")

	// org delete flags
	orgDeleteCmd.Flags().BoolVarP(&orgDeleteForce, "force", "f", false, "skip confirmation")

	// org sso subcommands
	orgSSOCmd.AddCommand(orgSSOGetCmd)
	orgSSOCmd.AddCommand(orgSSOSetCmd)
	orgSSOCmd.AddCommand(orgSSODeleteCmd)
	orgSSOCmd.AddCommand(orgSSOMappingsCmd)

	// org sso set flags
	orgSSOSetCmd.Flags().StringVar(&orgSSOSetIssuer, "issuer", "", "OIDC issuer URL")
	orgSSOSetCmd.Flags().StringVar(&orgSSOSetClientID, "client-id", "", "OIDC client ID")
	orgSSOSetCmd.Flags().StringVar(&orgSSOSetClientSecret, "client-secret", "", "OIDC client secret (write-only; empty = leave unchanged)")
	orgSSOSetCmd.Flags().BoolVar(&orgSSOSetEnabled, "enabled", false, "enable SSO for this organization")
	orgSSOSetCmd.Flags().StringVar(&orgSSOSetScopes, "scopes", "", "space-separated OIDC scopes")
	orgSSOSetCmd.Flags().StringVar(&orgSSOSetGroupClaim, "group-claim", "", "claim name containing group list")
	orgSSOSetCmd.Flags().StringVar(&orgSSOSetDefaultRole, "default-role", "", "default role when no mapping matches (empty = deny)")

	// org sso mappings subcommands
	orgSSOMappingsCmd.AddCommand(orgSSOMappingsListCmd)
	orgSSOMappingsCmd.AddCommand(orgSSOMappingsSetCmd)

	// org sso mappings set flags
	orgSSOMappingsSetCmd.Flags().StringVar(&orgSSOMappingsSetFile, "file", "", `JSON file with mapping array ("-" for stdin)`)
	orgSSOMappingsSetCmd.Flags().StringArrayVar(&orgSSOMappingsSetMappings, "mapping", nil, "mapping spec: group_value:role:priority[@scope_id] (repeatable)")
}
