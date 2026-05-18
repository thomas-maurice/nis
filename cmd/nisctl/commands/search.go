package commands

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var searchCmd = &cobra.Command{
	Use:   "search QUERY",
	Short: "Global search across the identity tree",
	Long: `Search operators, accounts, users, scoped signing keys, and clusters
in one query. Matches names, descriptions, and public keys; for scoped
signing keys, also matches against the pub/sub allow/deny subject lists.

Results are narrowed to the caller's RBAC scope — operator-admins only
see their own operator's subtree.

Use --kind to limit the surfaces searched.`,
	Args: cobra.ExactArgs(1),
	RunE: runSearch,
}

var (
	searchKinds []string
	searchLimit int
)

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().StringSliceVar(&searchKinds, "kind", nil,
		"limit to these kinds (repeatable): operator, account, user, scoped-key, cluster")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "max results per kind (cap 100)")
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	kinds, err := parseSearchKinds(searchKinds)
	if err != nil {
		return err
	}

	resp, err := GetClient().Search.Search(context.Background(), connect.NewRequest(&nisv1.SearchRequest{
		Query: query,
		Kinds: kinds,
		Limit: int32(searchLimit),
	}))
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	printer := client.NewPrinter(GetOutputFormat())

	total := len(resp.Msg.Operators) + len(resp.Msg.Accounts) + len(resp.Msg.Users) +
		len(resp.Msg.ScopedSigningKeys) + len(resp.Msg.Clusters)
	if total == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No matches")
		}
		return nil
	}

	if GetOutputFormat() != "table" {
		return printer.PrintObject(resp.Msg)
	}

	// Table mode: print one section per non-empty kind.
	if len(resp.Msg.Operators) > 0 {
		printer.PrintMessage("OPERATORS (%d)", len(resp.Msg.Operators))
		rows := make([][]string, len(resp.Msg.Operators))
		for i, o := range resp.Msg.Operators {
			rows[i] = []string{o.Name, shortID(o.Id), shortKey(o.PublicKey), truncate(o.Description, 60)}
		}
		if err := printer.PrintTable([]string{"NAME", "ID", "PUBKEY", "DESCRIPTION"}, rows); err != nil {
			return err
		}
	}
	if len(resp.Msg.Accounts) > 0 {
		printer.PrintMessage("ACCOUNTS (%d)", len(resp.Msg.Accounts))
		rows := make([][]string, len(resp.Msg.Accounts))
		for i, a := range resp.Msg.Accounts {
			rows[i] = []string{a.Name, shortID(a.Id), shortID(a.OperatorId), shortKey(a.PublicKey), truncate(a.Description, 40)}
		}
		if err := printer.PrintTable([]string{"NAME", "ID", "OPERATOR", "PUBKEY", "DESCRIPTION"}, rows); err != nil {
			return err
		}
	}
	if len(resp.Msg.Users) > 0 {
		printer.PrintMessage("USERS (%d)", len(resp.Msg.Users))
		rows := make([][]string, len(resp.Msg.Users))
		for i, u := range resp.Msg.Users {
			rows[i] = []string{u.Name, shortID(u.Id), shortID(u.AccountId), shortKey(u.PublicKey), truncate(u.Description, 40)}
		}
		if err := printer.PrintTable([]string{"NAME", "ID", "ACCOUNT", "PUBKEY", "DESCRIPTION"}, rows); err != nil {
			return err
		}
	}
	if len(resp.Msg.ScopedSigningKeys) > 0 {
		printer.PrintMessage("SCOPED SIGNING KEYS (%d)", len(resp.Msg.ScopedSigningKeys))
		rows := make([][]string, len(resp.Msg.ScopedSigningKeys))
		for i, k := range resp.Msg.ScopedSigningKeys {
			rows[i] = []string{k.Name, shortID(k.Id), shortID(k.AccountId), shortKey(k.PublicKey), permsSummary(k)}
		}
		if err := printer.PrintTable([]string{"NAME", "ID", "ACCOUNT", "PUBKEY", "PERMISSIONS"}, rows); err != nil {
			return err
		}
	}
	if len(resp.Msg.Clusters) > 0 {
		printer.PrintMessage("CLUSTERS (%d)", len(resp.Msg.Clusters))
		rows := make([][]string, len(resp.Msg.Clusters))
		for i, c := range resp.Msg.Clusters {
			rows[i] = []string{c.Name, shortID(c.Id), shortID(c.OperatorId), strings.Join(c.ServerUrls, ", ")}
		}
		if err := printer.PrintTable([]string{"NAME", "ID", "OPERATOR", "URLS"}, rows); err != nil {
			return err
		}
	}

	return nil
}

// parseSearchKinds maps CLI flag values to proto enums. Accepts the obvious
// human spellings; rejects unknown values so a typo doesn't silently expand
// to "search everything".
func parseSearchKinds(in []string) ([]nisv1.SearchKind, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]nisv1.SearchKind, 0, len(in))
	for _, raw := range in {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "operator", "operators":
			out = append(out, nisv1.SearchKind_SEARCH_KIND_OPERATOR)
		case "account", "accounts":
			out = append(out, nisv1.SearchKind_SEARCH_KIND_ACCOUNT)
		case "user", "users":
			out = append(out, nisv1.SearchKind_SEARCH_KIND_USER)
		case "scoped-key", "scoped_key", "scopedkey", "scoped-signing-key":
			out = append(out, nisv1.SearchKind_SEARCH_KIND_SCOPED_SIGNING_KEY)
		case "cluster", "clusters":
			out = append(out, nisv1.SearchKind_SEARCH_KIND_CLUSTER)
		default:
			return nil, fmt.Errorf("unknown kind %q (allowed: operator, account, user, scoped-key, cluster)", raw)
		}
	}
	return out, nil
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func shortKey(k string) string {
	if len(k) <= 12 {
		return k
	}
	return k[:8] + "..." + k[len(k)-4:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// permsSummary builds a one-line "PA=N PD=N SA=N SD=N" tag from the scoped
// signing key's permission arrays so the table stays single-line.
func permsSummary(k *nisv1.ScopedSigningKey) string {
	if k.Permissions == nil {
		return ""
	}
	return fmt.Sprintf("PA=%d PD=%d SA=%d SD=%d",
		len(k.Permissions.PubAllow),
		len(k.Permissions.PubDeny),
		len(k.Permissions.SubAllow),
		len(k.Permissions.SubDeny),
	)
}
