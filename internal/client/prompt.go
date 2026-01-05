package client

import (
	"fmt"
	"syscall"

	"golang.org/x/term"
)

// PromptPassword prompts the user for a password without echoing input
func PromptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println() // Print newline after password input
	if err != nil {
		return "", err
	}
	return string(passwordBytes), nil
}
