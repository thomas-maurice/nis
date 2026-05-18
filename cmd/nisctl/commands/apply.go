package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thomas-maurice/nis/pkg/manifest"
)

const (
	applyColorGreen = "\033[32m"
	applyColorRed   = "\033[31m"
	applyColorGray  = "\033[90m"
	applyColorReset = "\033[0m"
)

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a manifest to the NIS server",
	Long: `Apply creates or updates NIS entities declared in YAML manifest files.

Files may be individual YAML files, directories (recursed for *.yaml/*.yml),
or - for stdin. Multiple -f flags are accepted.

Without --dry-run the command prints the plan and asks for confirmation before
making any changes.`,
	RunE: runApply,
}

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show what would change if a manifest were applied (dry-run)",
	Long: `diff is equivalent to 'apply --dry-run': it computes and prints the plan
without making any server-side changes.`,
	RunE: runDiff,
}

var (
	applyFiles  []string
	diffFiles   []string
	applyDryRun bool
	applyYes    bool
)

func init() {
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(diffCmd)

	applyCmd.Flags().StringArrayVarP(&applyFiles, "filename", "f", nil, "file, directory, or - for stdin (repeatable)")
	_ = applyCmd.MarkFlagRequired("filename")
	applyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "print the plan without making changes")
	applyCmd.Flags().BoolVarP(&applyYes, "yes", "y", false, "skip confirmation prompt")

	diffCmd.Flags().StringArrayVarP(&diffFiles, "filename", "f", nil, "file, directory, or - for stdin (repeatable)")
	_ = diffCmd.MarkFlagRequired("filename")
}

func runApply(cmd *cobra.Command, args []string) error {
	return runApplyOrDiff(cmd, applyFiles, false)
}

func runDiff(cmd *cobra.Command, args []string) error {
	return runApplyOrDiff(cmd, diffFiles, true)
}

func runApplyOrDiff(cmd *cobra.Command, files []string, dryRun bool) error {
	dryRun = dryRun || applyDryRun

	objs, err := manifest.Load(files)
	if err != nil {
		return err
	}

	if _, err := manifest.Validate(objs, manifest.ValidateOptions{StrictRefs: true}); err != nil {
		return err
	}

	adapter := manifest.NewClientAdapter(nisClient)
	ctx := cmd.Context()

	plan, err := manifest.Plan(ctx, adapter, objs)
	if err != nil {
		return err
	}

	if err := manifest.Print(plan, manifest.PrintOptions{Color: !noColor, W: os.Stdout}); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	if plan.Summary.Create+plan.Summary.Update == 0 {
		fmt.Println("Nothing to do.")
		return nil
	}

	if !applyYes {
		fmt.Print("Apply this plan? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		resp, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		resp = strings.TrimSpace(strings.ToLower(resp))
		if resp != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	result, applyErr := manifest.Apply(ctx, adapter, plan)

	created, updated, unchanged, failed := 0, 0, 0, 0
	for _, item := range result.Items {
		switch item.Outcome {
		case manifest.OutcomeApplied:
			switch item.Action {
			case manifest.ActionCreate:
				created++
				printApplyLine("[ok]", "Created", item.Object, item.Note)
			case manifest.ActionUpdate:
				updated++
				printApplyLine("[ok]", "Updated", item.Object, item.Note)
			}
		case manifest.OutcomeNoop:
			unchanged++
			printApplyNoop(item)
		case manifest.OutcomeFailed:
			failed++
			printApplyFailed(item)
		}
	}

	fmt.Printf("\n%d created, %d updated, %d unchanged, %d failed.\n", created, updated, unchanged, failed)

	if applyErr != nil {
		return fmt.Errorf("apply failed: %w", applyErr)
	}
	return nil
}

func printApplyLine(prefix, verb string, obj manifest.Object, note string) {
	green := colorFn(applyColorGreen)
	label := green(fmt.Sprintf("%-8s", prefix))
	line := fmt.Sprintf("%s %s %s/%s%s", label, verb, obj.Kind, obj.Metadata.Name, applyParentSuffix(obj))
	if note != "" {
		line += " (" + note + ")"
	}
	fmt.Println(line)
}

func printApplyNoop(item manifest.ApplyItem) {
	gray := colorFn(applyColorGray)
	label := gray(fmt.Sprintf("%-8s", "[noop]"))
	line := fmt.Sprintf("%s %s/%s%s", label, item.Object.Kind, item.Object.Metadata.Name, applyParentSuffix(item.Object))
	if item.Note != "" {
		line += " (" + item.Note + ")"
	}
	fmt.Println(line)
}

func printApplyFailed(item manifest.ApplyItem) {
	red := colorFn(applyColorRed)
	label := red(fmt.Sprintf("%-8s", "[failed]"))
	line := fmt.Sprintf("%s %s/%s%s", label, item.Object.Kind, item.Object.Metadata.Name, applyParentSuffix(item.Object))
	if item.Note != "" {
		line += ": " + item.Note
	}
	fmt.Println(line)
}

func applyParentSuffix(obj manifest.Object) string {
	var parts []string
	if obj.Metadata.Operator != "" {
		parts = append(parts, "operator: "+obj.Metadata.Operator)
	}
	if obj.Metadata.Account != "" {
		parts = append(parts, "account: "+obj.Metadata.Account)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// colorFn returns a function that wraps s in an ANSI color when color is enabled.
func colorFn(code string) func(string) string {
	if noColor {
		return func(s string) string { return s }
	}
	return func(s string) string { return code + s + applyColorReset }
}
