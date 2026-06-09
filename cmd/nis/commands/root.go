package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "nis",
		Short: "NATS Identity Service - Manage NATS operators, accounts, and users",
		Long: `NATS Identity Service (NIS) is a centralized service for managing
NATS authentication entities including operators, accounts, users,
scoped signing keys, and clusters.

It provides a gRPC API for managing these entities and generating
JWTs and credentials files for NATS authentication.`,
	}
)

// SetVersion sets the version string for the root command
func SetVersion(v string) {
	rootCmd.Version = v
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
}

func initConfig() {
	if err := loadConfig(cfgFile); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}

	// Apply --log-level if explicitly passed. PersistentFlags belong to the
	// root cmd, but cobra makes them visible on the subcommand's Flags() too,
	// so RunE handlers can call applyFlagOverrides on their own cmd. Doing it
	// here for log-level lets early-startup code read the right value without
	// every subcommand having to remember.
	if f := rootCmd.PersistentFlags().Lookup("log-level"); f != nil && f.Changed {
		viper.Set("log_level", f.Value.String())
	}
}

// loadConfig seeds defaults, wires env-var binding, and reads the config file
// into viper. cfgFile is the value of the --config flag ("" when not passed).
//
// Failure policy — a misconfigured server must never boot silently on defaults:
//   - With an explicit --config, ANY read failure (missing, unreadable,
//     malformed) is fatal: the operator named a file and expects it honored.
//   - In search mode (no --config), a genuinely absent config file is fine
//     (defaults + env drive the process), but a file that IS found and fails
//     to parse is still fatal.
func loadConfig(cfgFile string) error {
	// Defaults first: they sit below env vars and the config file in the
	// precedence chain (env/config beat them; explicit flags applied via
	// applyFlagOverrides beat env/config).
	registerConfigDefaults()

	explicit := cfgFile != ""
	if explicit {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	// Replace dots with underscores in env var names (e.g. auth.jwt_secret ->
	// AUTH_JWT_SECRET) so nested keys can be overridden from the environment.
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !explicit && errors.As(err, &notFound) {
			// Search mode with no config file present: defaults + env only.
			return nil
		}
		target := cfgFile
		if target == "" {
			target = "config.yaml (searched in .)"
		}
		return fmt.Errorf("failed to load config file %q: %w", target, err)
	}

	fmt.Println("Using config file:", viper.ConfigFileUsed())
	return nil
}
