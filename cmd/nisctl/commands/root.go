package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thomas-maurice/nis/internal/client"
)

var (
	// Global flags
	cfgFile   string
	serverURL string
	token     string
	outputFmt string
	noColor   bool
	quietMode bool

	// Global client instance
	nisClient *client.Client
)

var rootCmd = &cobra.Command{
	Use:   "nisctl",
	Short: "CLI client for NATS Identity Service",
	Long: `nisctl is a command-line client for interacting with the NATS Identity Service (NIS).

It provides commands for managing operators, accounts, users, scoped signing keys,
clusters, and API users through the NIS gRPC API.

Before using nisctl, you must login to the NIS server using the 'login' command.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip client initialization for commands that don't need it
		skipClientCommands := map[string]bool{
			"login":      true,
			"completion": true,
			"help":       true,
		}

		if skipClientCommands[cmd.Name()] {
			return nil
		}

		// Load config
		cfg, err := client.LoadConfig(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Override server URL if provided via flag
		if serverURL != "" {
			cfg.ServerURL = serverURL
		}

		// Override token if provided via flag
		if token != "" {
			cfg.Token = token
		}

		// Validate that we have a server URL and token
		if cfg.ServerURL == "" {
			return fmt.Errorf("server URL not configured. Please run 'nisctl login' first or use --server flag")
		}

		if cfg.Token == "" {
			return fmt.Errorf("not authenticated. Please run 'nisctl login' first or use --token flag")
		}

		// Create client
		nisClient, err = client.NewClient(cfg.ServerURL, cfg.Token)
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}

		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Close client connection if it was created
		if nisClient != nil {
			nisClient.Close()
		}
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/nisctl/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&serverURL, "server", "", "NIS server URL (e.g., http://localhost:8080)")
	rootCmd.PersistentFlags().StringVar(&token, "token", "", "authentication token")
	rootCmd.PersistentFlags().StringVarP(&outputFmt, "output", "o", "table", "output format (table, json, yaml, quiet)")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")
	rootCmd.PersistentFlags().BoolVarP(&quietMode, "quiet", "q", false, "quiet mode (minimal output)")
}

// GetClient returns the global client instance
func GetClient() *client.Client {
	return nisClient
}

// GetOutputFormat returns the configured output format
func GetOutputFormat() string {
	if quietMode {
		return "quiet"
	}
	return outputFmt
}
