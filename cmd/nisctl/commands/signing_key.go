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

var signingKeyCmd = &cobra.Command{
	Use:     "signing-key",
	Aliases: []string{"scoped-key", "sk"},
	Short:   "Manage scoped signing keys",
	Long:    `Create, list, and delete scoped signing keys for accounts.`,
}

var signingKeyCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a new scoped signing key",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyCreate,
}

var signingKeyListCmd = &cobra.Command{
	Use:   "list ACCOUNT_NAME",
	Short: "List scoped signing keys for an account",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyList,
}

var signingKeyGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get scoped signing key details",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyGet,
}

var signingKeyDeleteCmd = &cobra.Command{
	Use:   "delete ID",
	Short: "Delete a scoped signing key",
	Args:  cobra.ExactArgs(1),
	RunE:  runSigningKeyDelete,
}

var (
	signingKeyOperatorID      string
	signingKeyAccountID       string
	signingKeyForce           bool
	signingKeyFromTemplate    string
	signingKeyTemplateVersion int
	signingKeyTrackLatest     bool

	// Permission + metadata flags shared by `create` and `edit`. Permissions
	// reach the server via two different RPCs depending on the command:
	// create stuffs them straight into CreateScopedSigningKeyRequest;
	// edit fetches the current SSK first, overlays only the lists the
	// caller actually changed (Cobra's .Changed() drives the overlay),
	// and re-sends the full set through UpdatePermissions — which is
	// REPLACE-semantics on the server. Without the overlay, "edit --pub-allow
	// foo" would silently clear sub_allow / sub_deny / pub_deny.
	signingKeyDescription     string
	signingKeyName            string // edit-only: rename
	signingKeyPubAllow        []string
	signingKeyPubDeny         []string
	signingKeySubAllow        []string
	signingKeySubDeny         []string
	signingKeyResponseMaxMsgs int
	signingKeyResponseTTL     string
)

var signingKeyDetachTemplateCmd = &cobra.Command{
	Use:   "detach-template ID",
	Short: "Detach a scoped signing key from its template",
	Long: `Clear the SSK's template_id and template_version, leaving its
current permission columns untouched. After detach, the SSK becomes
standalone — future template updates have no effect on it, and the UI
stops rendering "From template X@vN" / outdated badges.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyDetachTemplate,
}

var signingKeyBumpTemplateCmd = &cobra.Command{
	Use:   "bump-template ID",
	Short: "Apply a template version to a scoped signing key",
	Long: `Snapshot a target template version's permissions into the SSK,
regenerate the parent account JWT, and push to every attached cluster.
When --to-version is unset, applies the template's current latest_version.
This is the explicit roll-out path — template updates never auto-cascade
to dependent SSKs.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyBumpTemplate,
}

var signingKeyBumpTargetVersion int
var signingKeyTrackLatestEnabled bool

var signingKeyTrackLatestCmd = &cobra.Command{
	Use:   "track-latest ID",
	Short: "Enable or disable auto-tracking of the bound template's latest version",
	Long: `Toggle the SSK's track_latest flag. When enabled, every UpdateTemplate
on the bound template auto-applies the new version to this SSK (re-signs
the parent account JWT, pushes to clusters). Enabling requires the SSK
to be templated AND clean (no drift); direct permission edits are
rejected while tracking is on so an auto-apply can't silently overwrite
operator changes.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyTrackLatest,
}

var signingKeyEditCmd = &cobra.Command{
	Use:     "edit ID",
	Aliases: []string{"update"},
	Short:   "Edit a scoped signing key's metadata and/or permissions",
	Long: `Partial update — only the flags you pass take effect.

Metadata edits (--name, --description) call UpdateScopedSigningKey and
do NOT regenerate the parent account JWT. Permission edits (--pub-allow,
--pub-deny, --sub-allow, --sub-deny, --response-max-msgs, --response-ttl)
call UpdatePermissions: the parent account JWT is re-signed and pushed
to every attached cluster on the next sync.

Permission RPC is REPLACE-semantics on the server, but this command does
partial-set: it fetches the current SSK, overlays only the lists you
named on the command line, and sends the full set back. Pass
"--pub-allow ''" (empty value) to clear a list explicitly.

Editing permissions on a templated SSK flips its template_drifted flag
and is rejected outright when track_latest is enabled — bump or detach
first.`,
	Args: cobra.ExactArgs(1),
	RunE: runSigningKeyEdit,
}

func init() {
	rootCmd.AddCommand(signingKeyCmd)

	signingKeyCmd.AddCommand(signingKeyCreateCmd)
	signingKeyCmd.AddCommand(signingKeyListCmd)
	signingKeyCmd.AddCommand(signingKeyGetCmd)
	signingKeyCmd.AddCommand(signingKeyEditCmd)
	signingKeyCmd.AddCommand(signingKeyDeleteCmd)
	signingKeyCmd.AddCommand(signingKeyDetachTemplateCmd)
	signingKeyCmd.AddCommand(signingKeyBumpTemplateCmd)
	signingKeyCmd.AddCommand(signingKeyTrackLatestCmd)
	signingKeyTrackLatestCmd.Flags().BoolVar(&signingKeyTrackLatestEnabled, "enabled", true, "true to enable tracking, false to disable")

	signingKeyCreateCmd.Flags().StringVar(&signingKeyOperatorID, "operator", "", "operator ID or name (required)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyAccountID, "account", "", "account name (required)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyDescription, "description", "", "human-readable description")
	signingKeyCreateCmd.Flags().StringSliceVar(&signingKeyPubAllow, "pub-allow", nil, "subject pattern allowed for publish (repeatable / comma-separated)")
	signingKeyCreateCmd.Flags().StringSliceVar(&signingKeyPubDeny, "pub-deny", nil, "subject pattern denied for publish (repeatable / comma-separated)")
	signingKeyCreateCmd.Flags().StringSliceVar(&signingKeySubAllow, "sub-allow", nil, "subject pattern allowed for subscribe (repeatable / comma-separated)")
	signingKeyCreateCmd.Flags().StringSliceVar(&signingKeySubDeny, "sub-deny", nil, "subject pattern denied for subscribe (repeatable / comma-separated)")
	signingKeyCreateCmd.Flags().IntVar(&signingKeyResponseMaxMsgs, "response-max-msgs", 0, "max reply messages on auto-granted inbox replies (0 = NATS default of 1; only emitted when there's a restricted pub-allow or any response cap is set — see SKILL Scoped-signer Resp emission rule)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyResponseTTL, "response-ttl", "", "reply inbox TTL on auto-granted responses (e.g. 30s; empty = no explicit cap)")
	signingKeyCreateCmd.Flags().StringVar(&signingKeyFromTemplate, "from-template", "", "create the SSK from this operator-scoped template (snapshots its permissions; mutually exclusive with the perm flags above)")
	signingKeyCreateCmd.Flags().IntVar(&signingKeyTemplateVersion, "template-version", 0, "pin to a specific template version (0 = current latest)")
	signingKeyCreateCmd.Flags().BoolVar(&signingKeyTrackLatest, "track-latest", false, "auto-apply every new template version to this SSK (requires --from-template; ignores --template-version)")
	_ = signingKeyCreateCmd.MarkFlagRequired("operator")
	_ = signingKeyCreateCmd.MarkFlagRequired("account")

	signingKeyEditCmd.Flags().StringVar(&signingKeyName, "name", "", "new name for the SSK")
	signingKeyEditCmd.Flags().StringVar(&signingKeyDescription, "description", "", "new description")
	signingKeyEditCmd.Flags().StringSliceVar(&signingKeyPubAllow, "pub-allow", nil, "replace the pub allow list (empty value clears it)")
	signingKeyEditCmd.Flags().StringSliceVar(&signingKeyPubDeny, "pub-deny", nil, "replace the pub deny list")
	signingKeyEditCmd.Flags().StringSliceVar(&signingKeySubAllow, "sub-allow", nil, "replace the sub allow list")
	signingKeyEditCmd.Flags().StringSliceVar(&signingKeySubDeny, "sub-deny", nil, "replace the sub deny list")
	signingKeyEditCmd.Flags().IntVar(&signingKeyResponseMaxMsgs, "response-max-msgs", 0, "new response-max-msgs cap")
	signingKeyEditCmd.Flags().StringVar(&signingKeyResponseTTL, "response-ttl", "", "new response TTL (Go duration; pass '' to clear)")

	signingKeyListCmd.Flags().StringVar(&signingKeyOperatorID, "operator", "", "operator ID or name (required)")
	_ = signingKeyListCmd.MarkFlagRequired("operator")

	signingKeyDeleteCmd.Flags().BoolVarP(&signingKeyForce, "force", "f", false, "skip confirmation prompt")

	signingKeyBumpTemplateCmd.Flags().IntVar(&signingKeyBumpTargetVersion, "to-version", 0, "target template version (0 = current latest)")
}

func runSigningKeyCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(signingKeyOperatorID)
	if err != nil {
		return err
	}

	// Get account by name
	accountReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       signingKeyAccountID,
	})

	accountResp, err := GetClient().Account.GetAccountByName(context.Background(), accountReq)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	if signingKeyTrackLatest && signingKeyFromTemplate == "" {
		return fmt.Errorf("--track-latest requires --from-template")
	}

	// Mutual exclusion: --from-template snapshots a template's perms into
	// the new SSK. Passing perm flags at the same time would be misleading
	// — the server ignores the perm fields in that case (see scoped_key.proto
	// docstring on CreateScopedSigningKeyRequest.template). Fail loud rather
	// than silently dropping what the caller asked for.
	if signingKeyFromTemplate != "" {
		for _, name := range []string{"pub-allow", "pub-deny", "sub-allow", "sub-deny", "response-max-msgs", "response-ttl"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--from-template snapshots the template's permissions; passing --%s alongside it would be silently ignored. Drop --from-template if you want explicit perms, or drop --%s if you want the template", name, name)
			}
		}
	}

	createReq := &nisv1.CreateScopedSigningKeyRequest{
		AccountId:   accountResp.Msg.Account.Id,
		Name:        name,
		Description: signingKeyDescription,
		TrackLatest: signingKeyTrackLatest,
	}

	if signingKeyFromTemplate != "" {
		createReq.Template = &nisv1.TemplateRef{
			OperatorId:    operatorID,
			TemplateName:  signingKeyFromTemplate,
			VersionNumber: int32(signingKeyTemplateVersion),
		}
	} else if anySSKPermFlagSet(cmd) {
		// Only attach the proto sub-messages when the caller actually asked
		// for permissions. An always-non-nil ResponsePermission would force
		// jwt_service.go to emit a Resp clause on the SSK — see the
		// "Scoped-signer Resp emission rule" in SKILL.md.
		createReq.Permissions = &nisv1.UserPermissions{
			PubAllow: signingKeyPubAllow,
			PubDeny:  signingKeyPubDeny,
			SubAllow: signingKeySubAllow,
			SubDeny:  signingKeySubDeny,
		}
		if cmd.Flags().Changed("response-max-msgs") || cmd.Flags().Changed("response-ttl") {
			ttl, err := parseSSKTTL(signingKeyResponseTTL)
			if err != nil {
				return err
			}
			createReq.ResponsePermission = &nisv1.ResponsePermission{
				MaxMsgs: int32(signingKeyResponseMaxMsgs),
				Expires: ttl,
			}
		}
	}
	req := connect.NewRequest(createReq)

	resp, err := GetClient().ScopedSigningKey.CreateScopedSigningKey(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}

	printer.PrintSuccess("Scoped signing key created successfully")
	return printer.PrintObject(resp.Msg.Key)
}

// anySSKPermFlagSet returns true iff the caller passed any of the
// permission-shape flags (pub/sub allow/deny + response caps). Used to
// decide whether to attach proto sub-messages on create — passing them
// unconditionally would interact badly with the Scoped-signer Resp
// emission rule (see SKILL).
func anySSKPermFlagSet(cmd *cobra.Command) bool {
	for _, name := range []string{"pub-allow", "pub-deny", "sub-allow", "sub-deny", "response-max-msgs", "response-ttl"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// parseSSKTTL converts a Go duration string into nanoseconds for the
// ResponsePermission.Expires wire field. Empty / "0" / "0s" → 0 (no cap).
// Mirrors parseTemplateTTL in template.go so the two surfaces accept
// identical inputs.
func parseSSKTTL(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0s" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --response-ttl %q: %w", s, err)
	}
	return int64(d), nil
}

func runSigningKeyEdit(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	metaChanged := cmd.Flags().Changed("name") || cmd.Flags().Changed("description")
	permsChanged := anySSKPermFlagSet(cmd)
	if !metaChanged && !permsChanged {
		return fmt.Errorf("nothing to do: pass --name / --description for metadata, or --pub-allow / --pub-deny / --sub-allow / --sub-deny / --response-max-msgs / --response-ttl for permissions")
	}

	// We need the current SSK regardless: metadata-edit prints it back,
	// and permission-edit overlays unset lists.
	getResp, err := GetClient().ScopedSigningKey.GetScopedSigningKey(context.Background(),
		connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{Id: id}))
	if err != nil {
		return fmt.Errorf("scoped signing key not found: %w", err)
	}
	current := getResp.Msg.Key
	finalKey := current

	if metaChanged {
		metaReq := &nisv1.UpdateScopedSigningKeyRequest{Id: id}
		if cmd.Flags().Changed("name") {
			metaReq.Name = &signingKeyName
		}
		if cmd.Flags().Changed("description") {
			metaReq.Description = &signingKeyDescription
		}
		resp, err := GetClient().ScopedSigningKey.UpdateScopedSigningKey(context.Background(), connect.NewRequest(metaReq))
		if err != nil {
			return fmt.Errorf("failed to update scoped signing key metadata: %w", err)
		}
		finalKey = resp.Msg.Key
	}

	if permsChanged {
		// UpdatePermissions is REPLACE-semantics on the server (see
		// scoped_key_handler.go:266 — nil slice ⇒ cleared list). To support
		// partial updates from the CLI, overlay the unset lists with the
		// current values so they don't get wiped.
		var (
			curPerms = current.GetPermissions()
			curResp  = current.GetResponsePermission()
		)
		pubAllow := signingKeyPubAllow
		if !cmd.Flags().Changed("pub-allow") && curPerms != nil {
			pubAllow = curPerms.PubAllow
		}
		pubDeny := signingKeyPubDeny
		if !cmd.Flags().Changed("pub-deny") && curPerms != nil {
			pubDeny = curPerms.PubDeny
		}
		subAllow := signingKeySubAllow
		if !cmd.Flags().Changed("sub-allow") && curPerms != nil {
			subAllow = curPerms.SubAllow
		}
		subDeny := signingKeySubDeny
		if !cmd.Flags().Changed("sub-deny") && curPerms != nil {
			subDeny = curPerms.SubDeny
		}

		maxMsgs := int32(signingKeyResponseMaxMsgs)
		if !cmd.Flags().Changed("response-max-msgs") && curResp != nil {
			maxMsgs = curResp.MaxMsgs
		}
		expires := int64(0)
		if cmd.Flags().Changed("response-ttl") {
			n, err := parseSSKTTL(signingKeyResponseTTL)
			if err != nil {
				return err
			}
			expires = n
		} else if curResp != nil {
			expires = curResp.Expires
		}

		resp, err := GetClient().ScopedSigningKey.UpdatePermissions(context.Background(),
			connect.NewRequest(&nisv1.UpdatePermissionsRequest{
				Id: id,
				Permissions: &nisv1.UserPermissions{
					PubAllow: pubAllow,
					PubDeny:  pubDeny,
					SubAllow: subAllow,
					SubDeny:  subDeny,
				},
				ResponsePermission: &nisv1.ResponsePermission{
					MaxMsgs: maxMsgs,
					Expires: expires,
				},
			}))
		if err != nil {
			return fmt.Errorf("failed to update scoped signing key permissions: %w", err)
		}
		finalKey = resp.Msg.Key
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(finalKey.Id)
		return nil
	}

	switch {
	case metaChanged && permsChanged:
		printer.PrintSuccess("Scoped signing key '%s' updated (metadata + permissions)", finalKey.Name)
	case permsChanged:
		printer.PrintSuccess("Scoped signing key '%s' permissions updated; account JWT will be re-signed", finalKey.Name)
	default:
		printer.PrintSuccess("Scoped signing key '%s' metadata updated", finalKey.Name)
	}
	return printer.PrintObject(finalKey)
}

func runSigningKeyList(cmd *cobra.Command, args []string) error {
	accountName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Resolve operator ID
	operatorID, err := resolveOperatorID(signingKeyOperatorID)
	if err != nil {
		return err
	}

	// Get account by name
	accountReq := connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: operatorID,
		Name:       accountName,
	})

	accountResp, err := GetClient().Account.GetAccountByName(context.Background(), accountReq)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	req := connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
		AccountId: accountResp.Msg.Account.Id,
	})

	resp, err := GetClient().ScopedSigningKey.ListScopedSigningKeys(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to list scoped signing keys: %w", err)
	}

	if len(resp.Msg.Keys) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No scoped signing keys found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"ID", "NAME", "ACCOUNT", "CREATED AT"}
		rows := make([][]string, len(resp.Msg.Keys))

		for i, key := range resp.Msg.Keys {
			createdAt := "-"
			if key.CreatedAt != nil {
				createdAt = key.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
			}

			rows[i] = []string{
				client.ScopedKeyID(key.Id),
				key.Name,
				client.AccountID(key.AccountId),
				createdAt,
			}
		}

		return printer.PrintTable(headers, rows)
	}

	return printer.PrintList(resp.Msg.Keys)
}

func runSigningKeyGet(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	req := connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{
		Id: id,
	})

	resp, err := GetClient().ScopedSigningKey.GetScopedSigningKey(context.Background(), req)
	if err != nil {
		return fmt.Errorf("scoped signing key not found: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}

	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyDelete(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	getReq := connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{
		Id: id,
	})

	getResp, err := GetClient().ScopedSigningKey.GetScopedSigningKey(context.Background(), getReq)
	if err != nil {
		return fmt.Errorf("scoped signing key not found: %w", err)
	}

	if !signingKeyForce && GetOutputFormat() != "quiet" {
		if !client.ConfirmDeletion("scoped signing key", getResp.Msg.Key.Name) {
			printer.PrintMessage("Deletion cancelled")
			return nil
		}
	}

	req := connect.NewRequest(&nisv1.DeleteScopedSigningKeyRequest{
		Id: id,
	})

	_, err = GetClient().ScopedSigningKey.DeleteScopedSigningKey(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to delete scoped signing key: %w", err)
	}

	if GetOutputFormat() != "quiet" {
		printer.PrintSuccess("Scoped signing key '%s' deleted successfully", getResp.Msg.Key.Name)
	}

	return nil
}

func runSigningKeyDetachTemplate(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().ScopedSigningKey.DetachFromTemplate(context.Background(),
		connect.NewRequest(&nisv1.DetachFromTemplateRequest{Id: id}))
	if err != nil {
		return fmt.Errorf("failed to detach scoped signing key from template: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}
	printer.PrintSuccess("Scoped signing key '%s' detached from template", resp.Msg.Key.Name)
	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyBumpTemplate(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	resp, err := GetClient().Template.ApplyTemplateToScopedKey(context.Background(),
		connect.NewRequest(&nisv1.ApplyTemplateToScopedKeyRequest{
			ScopedSigningKeyId: id,
			VersionNumber:      int32(signingKeyBumpTargetVersion),
		}))
	if err != nil {
		return fmt.Errorf("failed to bump scoped signing key template: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}
	printer.PrintSuccess("Scoped signing key '%s' bumped to template version %d", resp.Msg.Key.Name, resp.Msg.Key.TemplateVersion)
	return printer.PrintObject(resp.Msg.Key)
}

func runSigningKeyTrackLatest(cmd *cobra.Command, args []string) error {
	id := args[0]
	printer := client.NewPrinter(GetOutputFormat())
	resp, err := GetClient().ScopedSigningKey.SetTrackLatest(context.Background(),
		connect.NewRequest(&nisv1.SetTrackLatestRequest{
			Id:      id,
			Enabled: signingKeyTrackLatestEnabled,
		}))
	if err != nil {
		return fmt.Errorf("failed to set track_latest: %w", err)
	}
	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.Key.Id)
		return nil
	}
	state := "disabled"
	if resp.Msg.Key.TrackLatest {
		state = "enabled"
	}
	printer.PrintSuccess("Scoped signing key '%s' track_latest %s", resp.Msg.Key.Name, state)
	return printer.PrintObject(resp.Msg.Key)
}
