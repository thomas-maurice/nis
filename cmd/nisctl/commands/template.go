package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var templateCmd = &cobra.Command{
	Use:     "template",
	Aliases: []string{"tpl"},
	Short:   "Manage permission templates (P6)",
	Long: `Create, list, get, update, delete operator-scoped permission templates,
list their version history and dependents.

Templates are immutable-versioned: every permission edit creates a new
version row; SKKs pinned to an older version stay on it until an
explicit bump via 'nisctl signing-key bump-template ID'.`,
}

var (
	tplOperatorID       string
	tplDescription      string
	tplPubAllow         []string
	tplPubDeny          []string
	tplSubAllow         []string
	tplSubDeny          []string
	tplResponseMaxMsgs  int
	tplResponseTTL      string
	tplChangeNote       string
	tplGetVersionNumber int
	tplForce            bool
)

var templateCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new template (version 1)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateCreate,
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List templates for an operator",
	RunE:  runTemplateList,
}

var templateGetCmd = &cobra.Command{
	Use:   "get NAME",
	Short: "Get a template (and one of its versions)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateGet,
}

var templateUpdateCmd = &cobra.Command{
	Use:   "update NAME",
	Short: "Update template metadata or create a new version",
	Long: `Description-only edits update in place. Permission edits create a
new template_versions row and bump latest_version. Pinned SKKs are
NOT touched — run 'nisctl signing-key bump-template ID' per SKK to
roll forward.`,
	Args: cobra.ExactArgs(1),
	RunE: runTemplateUpdate,
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "Delete a template (refused when any SKK pins it)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateDelete,
}

var templateVersionsCmd = &cobra.Command{
	Use:   "versions NAME",
	Short: "List all versions of a template",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateVersions,
}

var templateDependentsCmd = &cobra.Command{
	Use:   "dependents NAME",
	Short: "List SKKs currently pinned to this template",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateDependents,
}

func init() {
	rootCmd.AddCommand(templateCmd)

	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateGetCmd)
	templateCmd.AddCommand(templateUpdateCmd)
	templateCmd.AddCommand(templateDeleteCmd)
	templateCmd.AddCommand(templateVersionsCmd)
	templateCmd.AddCommand(templateDependentsCmd)

	// Permission flags shared by create + update. StringSliceVar accepts
	// repeated --pub-allow flags or a comma-separated value; matches the
	// existing nisctl convention for list-shaped fields.
	for _, c := range []*cobra.Command{templateCreateCmd, templateUpdateCmd} {
		c.Flags().StringVar(&tplOperatorID, "operator", "", "operator ID or name (required)")
		c.Flags().StringVar(&tplDescription, "description", "", "template description")
		c.Flags().StringSliceVar(&tplPubAllow, "pub-allow", nil, "publish allow subjects (repeatable)")
		c.Flags().StringSliceVar(&tplPubDeny, "pub-deny", nil, "publish deny subjects (repeatable)")
		c.Flags().StringSliceVar(&tplSubAllow, "sub-allow", nil, "subscribe allow subjects (repeatable)")
		c.Flags().StringSliceVar(&tplSubDeny, "sub-deny", nil, "subscribe deny subjects (repeatable)")
		c.Flags().IntVar(&tplResponseMaxMsgs, "response-max-msgs", 0, "max reply messages (0 = unlimited)")
		c.Flags().StringVar(&tplResponseTTL, "response-ttl", "", "reply inbox TTL (e.g. 30s; empty = no limit)")
		c.Flags().StringVar(&tplChangeNote, "change-note", "", "change note attached to new version row (ignored on description-only edits)")
		_ = c.MarkFlagRequired("operator")
	}

	templateListCmd.Flags().StringVar(&tplOperatorID, "operator", "", "operator ID or name (required)")
	_ = templateListCmd.MarkFlagRequired("operator")

	templateGetCmd.Flags().StringVar(&tplOperatorID, "operator", "", "operator ID or name (required)")
	templateGetCmd.Flags().IntVar(&tplGetVersionNumber, "version", 0, "version number (0 = latest)")
	_ = templateGetCmd.MarkFlagRequired("operator")

	templateDeleteCmd.Flags().StringVar(&tplOperatorID, "operator", "", "operator ID or name (required)")
	templateDeleteCmd.Flags().BoolVarP(&tplForce, "force", "f", false, "skip confirmation prompt")
	_ = templateDeleteCmd.MarkFlagRequired("operator")

	templateVersionsCmd.Flags().StringVar(&tplOperatorID, "operator", "", "operator ID or name (required)")
	_ = templateVersionsCmd.MarkFlagRequired("operator")

	templateDependentsCmd.Flags().StringVar(&tplOperatorID, "operator", "", "operator ID or name (required)")
	_ = templateDependentsCmd.MarkFlagRequired("operator")
}

func runTemplateCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	ttl, err := parseTemplateTTL(tplResponseTTL)
	if err != nil {
		return err
	}

	resp, err := GetClient().Template.CreateTemplate(context.Background(),
		connect.NewRequest(&nisv1.CreateTemplateRequest{
			OperatorId:  operatorID,
			Name:        name,
			Description: tplDescription,
			Permissions: &nisv1.UserPermissions{
				PubAllow: tplPubAllow,
				PubDeny:  tplPubDeny,
				SubAllow: tplSubAllow,
				SubDeny:  tplSubDeny,
			},
			ResponsePermission: &nisv1.ResponsePermission{
				MaxMsgs: int32(tplResponseMaxMsgs),
				Expires: ttl,
			},
			ChangeNote: tplChangeNote,
		}))
	if err != nil {
		return fmt.Errorf("failed to create template: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Template.Id)
		return nil
	}
	printer.PrintSuccess("Template '%s' created at version %d", resp.Msg.Template.Name, resp.Msg.Template.LatestVersion)
	return printer.PrintObject(resp.Msg.Template)
}

func runTemplateList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())
	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	resp, err := GetClient().Template.ListTemplates(context.Background(),
		connect.NewRequest(&nisv1.ListTemplatesRequest{OperatorId: operatorID}))
	if err != nil {
		return fmt.Errorf("failed to list templates: %w", err)
	}
	if len(resp.Msg.Templates) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No templates found")
		}
		return nil
	}
	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "LATEST VERSION", "DESCRIPTION"}
		rows := make([][]string, len(resp.Msg.Templates))
		for i, t := range resp.Msg.Templates {
			rows[i] = []string{t.Id[:8] + "...", t.Name, fmt.Sprintf("%d", t.LatestVersion), t.Description}
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(resp.Msg.Templates)
}

func runTemplateGet(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	resp, err := GetClient().Template.GetTemplateByName(context.Background(),
		connect.NewRequest(&nisv1.GetTemplateByNameRequest{
			OperatorId:    operatorID,
			Name:          name,
			VersionNumber: int32(tplGetVersionNumber),
		}))
	if err != nil {
		return fmt.Errorf("template not found: %w", err)
	}
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Template.Id)
		return nil
	}
	return printer.PrintObject(resp.Msg)
}

func runTemplateUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	getResp, err := GetClient().Template.GetTemplateByName(context.Background(),
		connect.NewRequest(&nisv1.GetTemplateByNameRequest{OperatorId: operatorID, Name: name}))
	if err != nil {
		return fmt.Errorf("template not found: %w", err)
	}
	ttl, err := parseTemplateTTL(tplResponseTTL)
	if err != nil {
		return err
	}
	// Mirror the SetJWTPolicy convention: send all the flags the caller
	// passed. PermissionsProvided semantics are inferred server-side by
	// looking at whether permissions/response_permission fields are set
	// to non-nil pointers; we always pass them populated. Description is
	// optional via the *string field.
	req := &nisv1.UpdateTemplateRequest{
		Id:          getResp.Msg.Template.Id,
		Description: &tplDescription,
		Permissions: &nisv1.UserPermissions{
			PubAllow: tplPubAllow,
			PubDeny:  tplPubDeny,
			SubAllow: tplSubAllow,
			SubDeny:  tplSubDeny,
		},
		ResponsePermission: &nisv1.ResponsePermission{
			MaxMsgs: int32(tplResponseMaxMsgs),
			Expires: ttl,
		},
		ChangeNote: tplChangeNote,
	}
	resp, err := GetClient().Template.UpdateTemplate(context.Background(), connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to update template: %w", err)
	}
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Template.Id)
		return nil
	}
	if resp.Msg.Version != nil {
		printer.PrintSuccess("Template '%s' updated; new version %d created", resp.Msg.Template.Name, resp.Msg.Version.VersionNumber)
	} else {
		printer.PrintSuccess("Template '%s' updated (description only — no version bump)", resp.Msg.Template.Name)
	}
	return printer.PrintObject(resp.Msg.Template)
}

func runTemplateDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	getResp, err := GetClient().Template.GetTemplateByName(context.Background(),
		connect.NewRequest(&nisv1.GetTemplateByNameRequest{OperatorId: operatorID, Name: name}))
	if err != nil {
		return fmt.Errorf("template not found: %w", err)
	}
	if !tplForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("template", name) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}
	if _, err := GetClient().Template.DeleteTemplate(context.Background(),
		connect.NewRequest(&nisv1.DeleteTemplateRequest{Id: getResp.Msg.Template.Id})); err != nil {
		return fmt.Errorf("failed to delete template: %w", err)
	}
	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Template '%s' deleted", name)
	}
	return nil
}

func runTemplateVersions(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	getResp, err := GetClient().Template.GetTemplateByName(context.Background(),
		connect.NewRequest(&nisv1.GetTemplateByNameRequest{OperatorId: operatorID, Name: name}))
	if err != nil {
		return fmt.Errorf("template not found: %w", err)
	}
	resp, err := GetClient().Template.ListTemplateVersions(context.Background(),
		connect.NewRequest(&nisv1.ListTemplateVersionsRequest{TemplateId: getResp.Msg.Template.Id}))
	if err != nil {
		return fmt.Errorf("failed to list template versions: %w", err)
	}
	if GetOutputFormat() == "table" {
		headers := []string{"VERSION", "CREATED AT", "CHANGE NOTE"}
		rows := make([][]string, len(resp.Msg.Versions))
		for i, v := range resp.Msg.Versions {
			created := "-"
			if v.CreatedAt != nil {
				created = v.CreatedAt.AsTime().Local().Format("2006-01-02 15:04:05")
			}
			rows[i] = []string{fmt.Sprintf("%d", v.VersionNumber), created, v.ChangeNote}
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(resp.Msg.Versions)
}

func runTemplateDependents(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	operatorID, err := resolveOperatorID(tplOperatorID)
	if err != nil {
		return err
	}
	getResp, err := GetClient().Template.GetTemplateByName(context.Background(),
		connect.NewRequest(&nisv1.GetTemplateByNameRequest{OperatorId: operatorID, Name: name}))
	if err != nil {
		return fmt.Errorf("template not found: %w", err)
	}
	resp, err := GetClient().Template.ListTemplateDependents(context.Background(),
		connect.NewRequest(&nisv1.ListTemplateDependentsRequest{TemplateId: getResp.Msg.Template.Id}))
	if err != nil {
		return fmt.Errorf("failed to list template dependents: %w", err)
	}
	if len(resp.Msg.Dependents) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No SKKs are pinned to this template")
		}
		return nil
	}
	if GetOutputFormat() == "table" {
		headers := []string{"SKK ID", "ACCOUNT", "SKK NAME", "PINNED VERSION", "DRIFTED?"}
		rows := make([][]string, len(resp.Msg.Dependents))
		for i, d := range resp.Msg.Dependents {
			drift := "no"
			if d.Drifted {
				drift = "YES"
			}
			rows[i] = []string{
				d.ScopedSigningKeyId[:8] + "...",
				d.AccountName,
				d.ScopedSigningKeyName,
				fmt.Sprintf("v%d", d.PinnedVersion),
				drift,
			}
		}
		return printer.PrintTable(headers, rows)
	}
	return printer.PrintList(resp.Msg.Dependents)
}

// parseTemplateTTL converts a duration string into nanoseconds for the
// ResponsePermission.Expires wire field. Empty string is zero (no limit).
func parseTemplateTTL(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0s" {
		return 0, nil
	}
	// Reuse the standard Go duration parser — same accepted formats as
	// the operator JWT-policy flags.
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --response-ttl %q: %w", s, err)
	}
	return int64(d), nil
}
