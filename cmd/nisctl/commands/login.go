package commands

import (
	"fmt"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/thomas-maurice/nis/internal/client"
	"golang.org/x/term"
)

var loginCmd = &cobra.Command{
	Use:   "login [SERVER_URL]",
	Short: "Login to the NIS server",
	Long: `Authenticate with the NIS server and save credentials locally.

The server URL should be in the format: http://localhost:8080

After successful login, your authentication token will be saved to
~/.config/nisctl/config.yaml and used for subsequent commands.`,
	Args: cobra.ExactArgs(1),
	RunE: runLogin,
}

var (
	loginUsername string
	loginPassword string
)

func init() {
	rootCmd.AddCommand(loginCmd)

	loginCmd.Flags().StringVarP(&loginUsername, "username", "u", "", "username for authentication")
	loginCmd.Flags().StringVarP(&loginPassword, "password", "p", "", "password for authentication (will prompt if not provided)")
}

func runLogin(cmd *cobra.Command, args []string) error {
	serverURL := args[0]

	// Prompt for username if not provided
	if loginUsername == "" {
		fmt.Print("Username: ")
		_, err := fmt.Scanln(&loginUsername)
		if err != nil {
			return fmt.Errorf("failed to read username: %w", err)
		}
	}

	// Prompt for password if not provided
	if loginPassword == "" {
		fmt.Print("Password: ")
		passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // Print newline after password input
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
		loginPassword = string(passwordBytes)
	}

	// Attempt login
	fmt.Printf("Logging in to %s...\n", serverURL)
	token, err := client.Login(serverURL, loginUsername, loginPassword)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// Save config
	cfg := &client.Config{
		ServerURL: serverURL,
		Token:     token,
	}

	if err := client.SaveConfig(cfg, cfgFile); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	configPath := cfgFile
	if configPath == "" {
		configPath, _ = client.DefaultConfigPath()
	}

	fmt.Printf("✓ Login successful\n")
	fmt.Printf("✓ Config saved to %s\n", configPath)

	return nil
}
