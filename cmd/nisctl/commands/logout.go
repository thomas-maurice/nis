package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thomas-maurice/nis/internal/client"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout from the NIS server",
	Long: `Remove locally stored authentication credentials.

This will delete your authentication token from ~/.config/nisctl/config.yaml.
You will need to login again to use nisctl.`,
	RunE: runLogout,
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}

func runLogout(cmd *cobra.Command, args []string) error {
	configPath := cfgFile
	if configPath == "" {
		var err error
		configPath, err = client.DefaultConfigPath()
		if err != nil {
			return err
		}
	}

	if err := client.ClearConfig(configPath); err != nil {
		return fmt.Errorf("failed to clear config: %w", err)
	}

	fmt.Println("✓ Logged out successfully")
	fmt.Printf("✓ Config removed from %s\n", configPath)

	return nil
}
