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
	viper.SetDefault("events.retention_days", 30)
	viper.SetDefault("webhooks.succeeded_retention_days", 7)
	viper.SetDefault("webhooks.poll_interval_seconds", 0)
	viper.SetDefault("webhooks.delivery_timeout_seconds", 10)
	viper.SetDefault("webhooks.max_attempts", 5)
	viper.SetDefault("webhooks.backoff_base_seconds", 10)
	viper.SetDefault("webhooks.backoff_cap_seconds", 600)
	viper.SetDefault("webhooks.shutdown_timeout_seconds", 30)
	viper.SetDefault("api_tokens.last_used_flush_interval_seconds", 30)
	// JWT lifecycle sweeper (P2). The interval defaults to 1h — short enough
	// to feel responsive after a policy change, long enough that the prune /
	// expiring-soon / expired phases don't churn the DB. Batch is the
	// per-phase row cap; 500 is a safe upper bound for one tick.
	viper.SetDefault("jwt_policy.sweep_interval_seconds", 3600)
	viper.SetDefault("jwt_policy.sweep_batch_limit", 500)
	// Jobs substrate (A2). poll_interval=0 means auto (2s Postgres, 10s
	// SQLite); lease_duration must exceed any handler's expected runtime
	// so a normal handler doesn't get its row reclaimed mid-execution.
	viper.SetDefault("jobs.poll_interval_seconds", 0)
	viper.SetDefault("jobs.claim_batch", 10)
	viper.SetDefault("jobs.lease_duration_seconds", 300)
	viper.SetDefault("jobs.shutdown_timeout_seconds", 30)
	viper.SetDefault("jobs.retention_days", 30)
}
