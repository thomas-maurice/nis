package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thomas-maurice/nis/internal/client"
	"github.com/thomas-maurice/nis/pkg/manifest"
)

var deleteManifestCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete NIS entities declared in a manifest",
	Long: `delete removes NIS entities declared in YAML manifest files.

Entities are deleted in reverse topological order (User → ScopedSigningKey →
Account → Cluster → Operator). Reserved entities ($SYS, system, default) are
listed but refused.

The command always prints what will be deleted and asks for confirmation
(unless --yes is given).`,
	RunE: runDeleteManifest,
}

var (
	deleteManifestFiles []string
	deleteManifestYes   bool
	deleteManifestOrg   string
)

func init() {
	rootCmd.AddCommand(deleteManifestCmd)

	deleteManifestCmd.Flags().StringArrayVarP(&deleteManifestFiles, "filename", "f", nil, "file, directory, or - for stdin (repeatable)")
	_ = deleteManifestCmd.MarkFlagRequired("filename")
	deleteManifestCmd.Flags().BoolVarP(&deleteManifestYes, "yes", "y", false, "skip confirmation prompt")
	deleteManifestCmd.Flags().StringVar(&deleteManifestOrg, "org", "", "target organization UUID (required for platform admins; ignored for org-scoped tokens)")
}

func runDeleteManifest(cmd *cobra.Command, args []string) error {
	objs, err := manifest.Load(deleteManifestFiles)
	if err != nil {
		return err
	}

	if _, err := manifest.Validate(objs, manifest.ValidateOptions{StrictRefs: false}); err != nil {
		return err
	}

	// Separate refused items from deletable ones.
	var deletable []manifest.Object
	var refused []manifest.Object
	for _, obj := range objs {
		if isReserved(obj) {
			refused = append(refused, obj)
		} else {
			deletable = append(deletable, obj)
		}
	}

	fmt.Println("The following entities will be deleted (in reverse topo order):")
	fmt.Println()
	for _, obj := range refused {
		fmt.Printf("  %s %s/%s%s\n", client.Gray("[refused]"), obj.Kind, obj.Metadata.Name, applyParentSuffix(obj))
	}
	for _, obj := range deletable {
		fmt.Printf("  %s %s/%s%s\n", client.Red("-"), obj.Kind, obj.Metadata.Name, applyParentSuffix(obj))
	}
	fmt.Println()

	if len(deletable) == 0 {
		fmt.Println("Nothing to delete.")
		return nil
	}

	if !deleteManifestYes {
		fmt.Print("Type 'yes' to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		resp, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		resp = strings.TrimSpace(strings.ToLower(resp))
		if resp != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	ctx := cmd.Context()
	adapter := manifest.NewClientAdapter(nisClient, deleteManifestOrg)

	result, deleteErr := manifest.DeleteAll(ctx, adapter, objs)

	deleted, alreadyGone, refusedCount, failed := 0, 0, 0, 0
	for _, item := range result.Items {
		switch item.Outcome {
		case manifest.OutcomeApplied:
			deleted++
			printDeleteLine("[ok]", "Deleted", item.Object, item.Note)
		case manifest.OutcomeNoop:
			alreadyGone++
			printDeleteNoop(item)
		case manifest.OutcomeFailed:
			if strings.Contains(item.Note, "refusing to delete reserved") {
				refusedCount++
				printDeleteRefused(item)
			} else {
				failed++
				printDeleteFailed(item)
			}
		}
	}

	fmt.Printf("\n%d deleted, %d already gone, %d refused, %d failed.\n", deleted, alreadyGone, refusedCount, failed)

	if deleteErr != nil {
		return fmt.Errorf("delete failed: %w", deleteErr)
	}
	return nil
}

func isReserved(obj manifest.Object) bool {
	switch obj.Kind {
	case manifest.KindAccount:
		return obj.Metadata.Name == "$SYS"
	case manifest.KindUser:
		return obj.Metadata.Name == "system"
	case manifest.KindScopedSigningKey:
		return obj.Metadata.Name == "default"
	}
	return false
}

func printDeleteLine(prefix, verb string, obj manifest.Object, note string) {
	label := client.Green(fmt.Sprintf("%-8s", prefix))
	line := fmt.Sprintf("%s %s %s/%s%s", label, verb, obj.Kind, obj.Metadata.Name, applyParentSuffix(obj))
	if note != "" {
		line += " (" + note + ")"
	}
	fmt.Println(line)
}

func printDeleteNoop(item manifest.DeleteItem) {
	label := client.Gray(fmt.Sprintf("%-8s", "[noop]"))
	line := fmt.Sprintf("%s %s/%s%s", label, item.Object.Kind, item.Object.Metadata.Name, applyParentSuffix(item.Object))
	if item.Note != "" {
		line += " (" + item.Note + ")"
	}
	fmt.Println(line)
}

func printDeleteRefused(item manifest.DeleteItem) {
	label := client.Gray(fmt.Sprintf("%-10s", "[refused]"))
	fmt.Printf("%s %s/%s%s\n", label, item.Object.Kind, item.Object.Metadata.Name, applyParentSuffix(item.Object))
}

func printDeleteFailed(item manifest.DeleteItem) {
	label := client.Red(fmt.Sprintf("%-8s", "[failed]"))
	line := fmt.Sprintf("%s %s/%s%s", label, item.Object.Kind, item.Object.Metadata.Name, applyParentSuffix(item.Object))
	if item.Note != "" {
		line += ": " + item.Note
	}
	fmt.Println(line)
}
