package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/pkg/manifest"
)

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Dump NIS state as YAML manifests",
}

var dumpOperatorCmd = &cobra.Command{
	Use:   "operator NAME",
	Short: "Dump an operator and its children as a multi-document YAML manifest",
	Long: `dump operator fetches an operator and all of its children (clusters, accounts,
scoped signing keys, users) and renders them as a multi-document YAML manifest
suitable for round-trip with 'nisctl apply'.

Reserved entities ($SYS account, system user) are excluded. The default
scoped signing key is included to allow round-trip permission management.`,
	Args: cobra.ExactArgs(1),
	RunE: runDumpOperator,
}

var (
	dumpOutput string
	dumpKinds  []string
)

var allKinds = []string{
	manifest.KindOperator,
	manifest.KindCluster,
	manifest.KindAccount,
	manifest.KindScopedSigningKey,
	manifest.KindUser,
}

func init() {
	rootCmd.AddCommand(dumpCmd)
	dumpCmd.AddCommand(dumpOperatorCmd)

	dumpOperatorCmd.Flags().StringVarP(&dumpOutput, "output", "o", "", "output file (default: stdout)")
	dumpOperatorCmd.Flags().StringSliceVar(&dumpKinds, "kinds", allKinds, "restrict output to these kinds (comma-separated)")
}

func runDumpOperator(cmd *cobra.Command, args []string) error {
	name := args[0]
	ctx := context.Background()

	// Validate kinds.
	kindSet := make(map[string]bool, len(dumpKinds))
	valid := make(map[string]bool, len(allKinds))
	for _, k := range allKinds {
		valid[k] = true
	}
	for _, k := range dumpKinds {
		k = strings.TrimSpace(k)
		if !valid[k] {
			return fmt.Errorf("unknown kind %q; valid kinds: %s", k, strings.Join(allKinds, ", "))
		}
		kindSet[k] = true
	}

	// Resolve operator by name.
	opResp, err := nisClient.Operator.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name: name,
	}))
	if err != nil {
		return fmt.Errorf("operator %q not found: %w", name, err)
	}
	op := opResp.Msg.GetOperator()

	// Fetch clusters.
	clResp, err := nisClient.Cluster.ListClusters(ctx, connect.NewRequest(&nisv1.ListClustersRequest{
		OperatorId: op.GetId(),
	}))
	if err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}

	// Fetch accounts.
	accResp, err := nisClient.Account.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{
		OperatorId: op.GetId(),
	}))
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}

	// Fetch all SKKs and users across non-$SYS accounts.
	var allSKKs []*nisv1.ScopedSigningKey
	var allUsers []*nisv1.User
	for _, acc := range accResp.Msg.GetAccounts() {
		if acc.GetName() == "$SYS" {
			continue
		}

		skkResp, err := nisClient.ScopedSigningKey.ListScopedSigningKeys(ctx, connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
			AccountId: acc.GetId(),
		}))
		if err != nil {
			return fmt.Errorf("list scoped signing keys for account %q: %w", acc.GetName(), err)
		}
		allSKKs = append(allSKKs, skkResp.Msg.GetKeys()...)

		userResp, err := nisClient.User.ListUsers(ctx, connect.NewRequest(&nisv1.ListUsersRequest{
			AccountId: acc.GetId(),
		}))
		if err != nil {
			return fmt.Errorf("list users for account %q: %w", acc.GetName(), err)
		}
		allUsers = append(allUsers, userResp.Msg.GetUsers()...)
	}

	// Fetch templates + their current versions for dump. ListTemplates
	// returns the parent rows only; we GetTemplate per row to pick up
	// the latest version's permission snapshot.
	var allTemplates []*manifest.TemplateWithCurrentVersion
	tplResp, err := nisClient.Template.ListTemplates(ctx, connect.NewRequest(&nisv1.ListTemplatesRequest{
		OperatorId: op.GetId(),
	}))
	if err != nil {
		return fmt.Errorf("list templates: %w", err)
	}
	for _, t := range tplResp.Msg.GetTemplates() {
		gtResp, err := nisClient.Template.GetTemplate(ctx, connect.NewRequest(&nisv1.GetTemplateRequest{
			Id: t.GetId(),
		}))
		if err != nil {
			return fmt.Errorf("get template %q latest version: %w", t.GetName(), err)
		}
		allTemplates = append(allTemplates, &manifest.TemplateWithCurrentVersion{
			Template: gtResp.Msg.GetTemplate(),
			Version:  gtResp.Msg.GetVersion(),
		})
	}

	objs := manifest.DumpObjects(op, clResp.Msg.GetClusters(), accResp.Msg.GetAccounts(), allSKKs, allUsers, allTemplates, kindSet)

	data, err := manifest.EncodeYAML(objs)
	if err != nil {
		return err
	}

	if dumpOutput != "" {
		if err := os.WriteFile(dumpOutput, data, 0600); err != nil {
			return fmt.Errorf("write %s: %w", dumpOutput, err)
		}
		return nil
	}

	_, err = os.Stdout.Write(data)
	return err
}
