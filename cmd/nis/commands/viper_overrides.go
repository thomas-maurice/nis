package commands

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// applyFlagOverrides copies values from explicitly-passed cobra flags into the
// matching viper keys.
//
// We avoid viper.BindPFlag because viper treats a flag's default value as if
// the user set it, so a flag with a non-empty default silently overrides
// config-file and env-var values for the same key. Defaults are registered
// via viper.SetDefault in registerConfigDefaults below; explicit flags hop in
// through this helper after cobra parsing.
//
// Precedence after this runs: explicit flag > env var > config file > default.
func applyFlagOverrides(cmd *cobra.Command, mapping map[string]string) {
	for flagName, viperKey := range mapping {
		flag := cmd.Flags().Lookup(flagName)
		if flag == nil || !flag.Changed {
			continue
		}
		// String form round-trips through spf13/cast inside viper.GetBool,
		// GetDuration, GetFloat64, GetInt, GetString — all the types we use.
		viper.Set(viperKey, flag.Value.String())
	}
}

// registerConfigDefaults seeds viper with the same defaults that the cobra
// flags carry, so a binary launched without flags and without a config file
// still has sensible values. Must be called once at process startup.
func registerConfigDefaults() {
	viper.SetDefault("server.address", ":8080")
	viper.SetDefault("server.enable_ui", true)
	viper.SetDefault("database.driver", "sqlite")
	viper.SetDefault("database.dsn", "nis.db")
	viper.SetDefault("database.auto_migrate", true)
	viper.SetDefault("encryption.key_id", "default")
	viper.SetDefault("auth.jwt_ttl", "24h")
	viper.SetDefault("metrics.enabled", true)
	viper.SetDefault("tracing.enabled", false)
	viper.SetDefault("tracing.endpoint", "localhost:4317")
	viper.SetDefault("tracing.insecure", true)
	viper.SetDefault("tracing.sample_ratio", 1.0)
	viper.SetDefault("tracing.service_name", "nis")
	viper.SetDefault("log_level", "info")
}
