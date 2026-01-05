package client

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Confirm prompts the user for confirmation before proceeding
// Returns true if the user confirms, false otherwise
func Confirm(message string) bool {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("%s [y/N]: ", message)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

// ConfirmDeletion prompts for confirmation before deleting a resource
func ConfirmDeletion(resourceType, resourceName string) bool {
	return Confirm(fmt.Sprintf("Are you sure you want to delete %s '%s'? This action cannot be undone.", resourceType, resourceName))
}
