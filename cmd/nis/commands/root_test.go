package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server told to use a specific --config must never boot on defaults when
// that file is broken — that silent fallback is exactly what masked a
// missing server.public_url in production.
func TestLoadConfig_ExplicitFile(t *testing.T) {
	t.Run("missing file is fatal", func(t *testing.T) {
		viper.Reset()
		err := loadConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
		require.Error(t, err)
	})

	t.Run("malformed file is fatal", func(t *testing.T) {
		viper.Reset()
		p := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(p, []byte("server: {address: \":8080\"\n  oops"), 0o600))
		err := loadConfig(p)
		require.Error(t, err)
	})

	t.Run("valid file loads and values are readable", func(t *testing.T) {
		viper.Reset()
		p := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(p, []byte("server:\n  public_url: \"https://nis.example.com\"\n"), 0o600))
		require.NoError(t, loadConfig(p))
		assert.Equal(t, "https://nis.example.com", viper.GetString("server.public_url"))
	})
}

func TestLoadConfig_SearchMode(t *testing.T) {
	t.Run("absent config is tolerated", func(t *testing.T) {
		viper.Reset()
		t.Chdir(t.TempDir())
		require.NoError(t, loadConfig(""))
	})

	t.Run("malformed config that IS found is fatal", func(t *testing.T) {
		viper.Reset()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("\tnot: valid"), 0o600))
		t.Chdir(dir)
		require.Error(t, loadConfig(""))
	})
}
