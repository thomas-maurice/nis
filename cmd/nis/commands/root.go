package commands

import (
	"fmt"
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
	// Defaults first: they sit below env vars and config file in the
	// precedence chain (viper.SetDefault > _nothing else_; env/config beat
	// it; explicit flags applied via applyFlagOverrides beat env/config).
	registerConfigDefaults()

	if cfgFile != "" {
		// Use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// Search for config in current directory
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	// Read in environment variables
	// Replace dots with underscores in env var names (e.g., auth.jwt_secret -> AUTH_JWT_SECRET)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// If a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
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
