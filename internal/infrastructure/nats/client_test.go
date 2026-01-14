package nats

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "single line",
			input:    "ABCD1234",
			expected: []string{"ABCD1234"},
		},
		{
			name:     "multiple lines",
			input:    "ABCD1234\nEFGH5678\nIJKL9012",
			expected: []string{"ABCD1234", "EFGH5678", "IJKL9012"},
		},
		{
			name:     "trailing newline",
			input:    "ABCD1234\nEFGH5678\n",
			expected: []string{"ABCD1234", "EFGH5678"},
		},
		{
			name:     "empty lines",
			input:    "ABCD1234\n\nEFGH5678",
			expected: []string{"ABCD1234", "", "EFGH5678"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitLines(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTrim(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no whitespace",
			input:    "ABCD1234",
			expected: "ABCD1234",
		},
		{
			name:     "leading spaces",
			input:    "  ABCD1234",
			expected: "ABCD1234",
		},
		{
			name:     "trailing spaces",
			input:    "ABCD1234  ",
			expected: "ABCD1234",
		},
		{
			name:     "both sides",
			input:    "  ABCD1234  ",
			expected: "ABCD1234",
		},
		{
			name:     "tabs and newlines",
			input:    "\t\nABCD1234\n\t",
			expected: "ABCD1234",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only whitespace",
			input:    "   \t\n  ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trim(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseCredsContent(t *testing.T) {
	validCreds := `-----BEGIN NATS USER JWT-----
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U
------END NATS USER JWT------

************************* IMPORTANT *************************
NKEY Seed printed below can be used to sign and prove identity.
NKEYs are sensitive and should be treated as secrets.

-----BEGIN USER NKEY SEED-----
SUACSSL3UAHUDXKFSNVUZRF5UHPMWZ6BFDTJ7M6USDXIEDNPPQYYYCU3VY
------END USER NKEY SEED------
`

	t.Run("valid credentials", func(t *testing.T) {
		jwt, seed, err := parseCredsContent(validCreds)
		assert.NoError(t, err)
		assert.Equal(t, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", jwt)
		assert.Equal(t, "SUACSSL3UAHUDXKFSNVUZRF5UHPMWZ6BFDTJ7M6USDXIEDNPPQYYYCU3VY", seed)
	})

	t.Run("missing JWT start marker", func(t *testing.T) {
		invalidCreds := `No JWT here
-----BEGIN USER NKEY SEED-----
SUACSSL3UAHUDXKFSNVUZRF5UHPMWZ6BFDTJ7M6USDXIEDNPPQYYYCU3VY
------END USER NKEY SEED------`

		_, _, err := parseCredsContent(invalidCreds)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "JWT start marker not found")
	})

	t.Run("missing JWT end marker", func(t *testing.T) {
		invalidCreds := `-----BEGIN NATS USER JWT-----
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9
No end marker`

		_, _, err := parseCredsContent(invalidCreds)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "JWT end marker not found")
	})

	t.Run("missing seed start marker", func(t *testing.T) {
		invalidCreds := `-----BEGIN NATS USER JWT-----
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9
------END NATS USER JWT------
No seed here`

		_, _, err := parseCredsContent(invalidCreds)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "seed start marker not found")
	})

	t.Run("missing seed end marker", func(t *testing.T) {
		invalidCreds := `-----BEGIN NATS USER JWT-----
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9
------END NATS USER JWT------
-----BEGIN USER NKEY SEED-----
SUACSSL3UAHUDXKFSNVUZRF5UHPMWZ6BFDTJ7M6USDXIEDNPPQYYYCU3VY
No end marker`

		_, _, err := parseCredsContent(invalidCreds)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "seed end marker not found")
	})
}

func TestNewClient_NoServerURLs(t *testing.T) {
	_, err := NewClient(ClientConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one server URL is required")
}

func TestNewClientFromCreds_NoServerURLs(t *testing.T) {
	_, err := NewClientFromCreds(nil, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one server URL is required")

	_, err = NewClientFromCreds([]string{}, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one server URL is required")
}

// TestParseAccountListResponse tests parsing of resolver list responses
func TestParseAccountListResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected []string
	}{
		{
			name:     "single account",
			response: "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456789012345678901234",
			expected: []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ123456789012345678901234"},
		},
		{
			name:     "multiple accounts",
			response: "AAAA1111\nBBBB2222\nCCCC3333",
			expected: []string{"AAAA1111", "BBBB2222", "CCCC3333"},
		},
		{
			name:     "accounts with trailing newline",
			response: "AAAA1111\nBBBB2222\n",
			expected: []string{"AAAA1111", "BBBB2222"},
		},
		{
			name:     "accounts with whitespace",
			response: "  AAAA1111  \n  BBBB2222  ",
			expected: []string{"AAAA1111", "BBBB2222"},
		},
		{
			name:     "empty lines filtered",
			response: "AAAA1111\n\nBBBB2222\n\n",
			expected: []string{"AAAA1111", "BBBB2222"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result []string
			for _, line := range splitLines(tt.response) {
				line = trim(line)
				if line != "" {
					result = append(result, line)
				}
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}
