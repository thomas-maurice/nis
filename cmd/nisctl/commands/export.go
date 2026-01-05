package commands

import (
	"context"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export and import operators",
	Long:  `Export operators with all their accounts, users, and clusters. Import operators from exported data.`,
}

var exportOperatorCmd = &cobra.Command{
	Use:   "operator OPERATOR_ID_OR_NAME",
	Short: "Export an operator",
	Args:  cobra.ExactArgs(1),
	RunE:  runExportOperator,
}

var importOperatorCmd = &cobra.Command{
	Use:   "import FILE",
	Short: "Import an operator from exported JSON file",
	Args:  cobra.ExactArgs(1),
	RunE:  runImportOperator,
}

var importNSCCmd = &cobra.Command{
	Use:   "import-nsc ARCHIVE_FILE OPERATOR_NAME",
	Short: "Import an operator from NSC archive (.zip, .tar.gz, .tar.bz2)",
	Args:  cobra.ExactArgs(2),
	RunE:  runImportNSC,
}

var (
	exportIncludeSecrets bool
	exportOutput         string
	importRegenerateIDs  bool
)

func init() {
	rootCmd.AddCommand(exportCmd)

	exportCmd.AddCommand(exportOperatorCmd)
	exportCmd.AddCommand(importOperatorCmd)
	exportCmd.AddCommand(importNSCCmd)

	exportOperatorCmd.Flags().BoolVarP(&exportIncludeSecrets, "include-secrets", "s", false, "include encrypted seeds in export")
	exportOperatorCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "output file (default: stdout)")

	importOperatorCmd.Flags().BoolVarP(&importRegenerateIDs, "regenerate-ids", "r", false, "regenerate UUIDs (for copying operators)")
}

func runExportOperator(cmd *cobra.Command, args []string) error {
	operatorIDOrName := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Try to parse as UUID first, if that fails try by name
	var operatorID string
	getReq := connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: operatorIDOrName,
	})

	getResp, err := GetClient().Operator.GetOperator(context.Background(), getReq)
	if err != nil {
		// Try by name
		nameReq := connect.NewRequest(&nisv1.GetOperatorByNameRequest{
			Name: operatorIDOrName,
		})

		nameResp, nameErr := GetClient().Operator.GetOperatorByName(context.Background(), nameReq)
		if nameErr != nil {
			return fmt.Errorf("operator not found: %w", nameErr)
		}

		operatorID = nameResp.Msg.Operator.Id
	} else {
		operatorID = getResp.Msg.Operator.Id
	}

	// Export the operator
	req := connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId:     operatorID,
		IncludeSecrets: exportIncludeSecrets,
	})

	resp, err := GetClient().Export.ExportOperator(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to export operator: %w", err)
	}

	// Write to output file or stdout
	if exportOutput != "" {
		if err := os.WriteFile(exportOutput, resp.Msg.Data, 0600); err != nil {
			return fmt.Errorf("failed to write export file: %w", err)
		}
		if GetOutputFormat() != "quiet" {
			printer.PrintSuccess("Operator exported to %s", exportOutput)
		}
	} else {
		// Write to stdout
		fmt.Println(string(resp.Msg.Data))
	}

	return nil
}

func runImportOperator(cmd *cobra.Command, args []string) error {
	filename := args[0]
	printer := client.NewPrinter(GetOutputFormat())

	// Read the export file
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read import file: %w", err)
	}

	// Import the operator
	req := connect.NewRequest(&nisv1.ImportOperatorRequest{
		Data:          data,
		RegenerateIds: importRegenerateIDs,
	})

	resp, err := GetClient().Export.ImportOperator(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to import operator: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.OperatorId)
		return nil
	}

	printer.PrintSuccess("Operator imported successfully")
	fmt.Printf("Operator ID: %s\n", resp.Msg.OperatorId)

	return nil
}

func runImportNSC(cmd *cobra.Command, args []string) error {
	archiveFile := args[0]
	operatorName := args[1]
	printer := client.NewPrinter(GetOutputFormat())

	// Read archive file
	archiveData, err := os.ReadFile(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to read archive file: %w", err)
	}

	// Import from NSC
	req := connect.NewRequest(&nisv1.ImportFromNSCRequest{
		Data:         archiveData,
		OperatorName: operatorName,
	})

	resp, err := GetClient().Export.ImportFromNSC(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to import from NSC: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		printer.PrintID(resp.Msg.OperatorId)
		return nil
	}

	printer.PrintSuccess("Operator imported from NSC successfully")
	fmt.Printf("Operator ID: %s\n", resp.Msg.OperatorId)

	return nil
}
