package client

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// OutputFormat represents the output format type
type OutputFormat string

const (
	OutputFormatTable OutputFormat = "table"
	OutputFormatJSON  OutputFormat = "json"
	OutputFormatYAML  OutputFormat = "yaml"
	OutputFormatQuiet OutputFormat = "quiet"
)

// Printer handles formatted output
type Printer struct {
	format OutputFormat
	writer io.Writer
}

// NewPrinter creates a new output printer
func NewPrinter(format string) *Printer {
	return &Printer{
		format: OutputFormat(format),
		writer: os.Stdout,
	}
}

// PrintTable prints data in table format
func (p *Printer) PrintTable(headers []string, rows [][]string) error {
	if p.format == OutputFormatQuiet {
		return nil
	}

	if p.format == OutputFormatJSON {
		return p.printJSON(convertTableToMap(headers, rows))
	}

	if p.format == OutputFormatYAML {
		return p.printYAML(convertTableToMap(headers, rows))
	}

	// Table format
	w := tabwriter.NewWriter(p.writer, 0, 0, 2, ' ', 0)
	defer w.Flush()

	// Print headers
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Repeat("-", len(strings.Join(headers, "\t"))))

	// Print rows
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}

	return nil
}

// PrintObject prints a single object
func (p *Printer) PrintObject(obj interface{}) error {
	if p.format == OutputFormatQuiet {
		return nil
	}

	if p.format == OutputFormatJSON {
		return p.printJSON(obj)
	}

	if p.format == OutputFormatYAML {
		return p.printYAML(obj)
	}

	// Default to YAML for single objects in table mode
	return p.printYAML(obj)
}

// PrintList prints a list of objects
func (p *Printer) PrintList(items interface{}) error {
	if p.format == OutputFormatQuiet {
		return nil
	}

	if p.format == OutputFormatJSON {
		return p.printJSON(items)
	}

	if p.format == OutputFormatYAML {
		return p.printYAML(items)
	}

	// Table format - delegate to PrintTable
	// This is a fallback; specific commands should use PrintTable directly
	return p.printYAML(items)
}

// PrintMessage prints a simple message
func (p *Printer) PrintMessage(format string, args ...interface{}) {
	if p.format == OutputFormatQuiet {
		return
	}
	fmt.Fprintf(p.writer, format+"\n", args...)
}

// PrintSuccess prints a success message
func (p *Printer) PrintSuccess(format string, args ...interface{}) {
	if p.format == OutputFormatQuiet {
		return
	}
	fmt.Fprintf(p.writer, "✓ "+format+"\n", args...)
}

// PrintError prints an error message
func (p *Printer) PrintError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
}

// PrintWarning prints a warning message
func (p *Printer) PrintWarning(format string, args ...interface{}) {
	if p.format == OutputFormatQuiet {
		return
	}
	fmt.Fprintf(p.writer, "⚠ "+format+"\n", args...)
}

// PrintID prints just an ID (useful for quiet mode scripts)
func (p *Printer) PrintID(id string) {
	fmt.Fprintln(p.writer, id)
}

func (p *Printer) printJSON(obj interface{}) error {
	encoder := json.NewEncoder(p.writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(obj)
}

func (p *Printer) printYAML(obj interface{}) error {
	encoder := yaml.NewEncoder(p.writer)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(obj)
}

func convertTableToMap(headers []string, rows [][]string) []map[string]string {
	result := make([]map[string]string, len(rows))
	for i, row := range rows {
		m := make(map[string]string)
		for j, header := range headers {
			if j < len(row) {
				m[header] = row[j]
			}
		}
		result[i] = m
	}
	return result
}
