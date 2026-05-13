package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyFlagOverrides_FlagDefaultDoesNotShadowConfig is the regression test
// for the viper.BindPFlag trap. Before this fix, calling viper.BindPFlag for a
// flag with a non-empty default would let that default silently override any
// value set via viper.Set (or read from config file / env var). applyFlagOverrides
// only writes to viper when the flag was *explicitly* passed by the user, so a
// config-file or env-var value survives.
func TestApplyFlagOverrides_FlagDefaultDoesNotShadowConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("db-dsn", "default-from-flag.db", "database connection string")

	// Simulate config file having set the value (this is what
	// viper.ReadInConfig would do).
	viper.Set("database.dsn", "from-config-file.db")

	// User did NOT pass --db-dsn on the command line, so flag.Changed is false.
	applyFlagOverrides(cmd, map[string]string{"db-dsn": "database.dsn"})

	assert.Equal(t, "from-config-file.db", viper.GetString("database.dsn"),
		"flag default must not override a config-file value when the flag was not passed")
}

func TestApplyFlagOverrides_ExplicitFlagWinsOverConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("db-dsn", "default.db", "database connection string")

	viper.Set("database.dsn", "from-config-file.db")

	// Simulate the user passing --db-dsn explicitly.
	require.NoError(t, cmd.Flags().Set("db-dsn", "explicit.db"))

	applyFlagOverrides(cmd, map[string]string{"db-dsn": "database.dsn"})

	assert.Equal(t, "explicit.db", viper.GetString("database.dsn"),
		"explicitly passed flag must win over config-file value")
}

func TestApplyFlagOverrides_BoolAndDuration(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("auto-migrate", true, "")
	cmd.Flags().String("jwt-ttl", "24h", "")

	require.NoError(t, cmd.Flags().Set("auto-migrate", "false"))
	require.NoError(t, cmd.Flags().Set("jwt-ttl", "1h"))

	applyFlagOverrides(cmd, map[string]string{
		"auto-migrate": "database.auto_migrate",
		"jwt-ttl":      "auth.jwt_ttl",
	})

	assert.False(t, viper.GetBool("database.auto_migrate"))
	assert.Equal(t, "1h0m0s", viper.GetDuration("auth.jwt_ttl").String())
}

func TestRegisterConfigDefaults(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	registerConfigDefaults()

	assert.Equal(t, ":8080", viper.GetString("server.address"))
	assert.Equal(t, "sqlite", viper.GetString("database.driver"))
	assert.Equal(t, "nis.db", viper.GetString("database.dsn"))
	assert.True(t, viper.GetBool("database.auto_migrate"))
	assert.True(t, viper.GetBool("server.enable_ui"))
	assert.True(t, viper.GetBool("metrics.enabled"))
	assert.False(t, viper.GetBool("tracing.enabled"))
}
