package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSyncResult_Empty(t *testing.T) {
	result := &SyncResult{
		Accounts:        make([]string, 0),
		RemovedAccounts: make([]string, 0),
		Errors:          make([]SyncError, 0),
	}

	assert.Equal(t, 0, result.AccountsAdded)
	assert.Equal(t, 0, result.AccountsRemoved)
	assert.Equal(t, 0, result.AccountsUpdated)
	assert.Empty(t, result.Accounts)
	assert.Empty(t, result.RemovedAccounts)
	assert.Empty(t, result.Errors)
}

func TestSyncResult_WithAccounts(t *testing.T) {
	result := &SyncResult{
		Accounts:        []string{"account1", "account2"},
		AccountsAdded:   1,
		AccountsRemoved: 0,
		AccountsUpdated: 2,
		RemovedAccounts: []string{},
		Errors:          []SyncError{},
	}

	assert.Equal(t, 2, len(result.Accounts))
	assert.Equal(t, 1, result.AccountsAdded)
	assert.Equal(t, 0, result.AccountsRemoved)
	assert.Equal(t, 2, result.AccountsUpdated)
}

func TestSyncResult_WithPruning(t *testing.T) {
	result := &SyncResult{
		Accounts:        []string{"account1"},
		AccountsAdded:   0,
		AccountsRemoved: 2,
		AccountsUpdated: 1,
		RemovedAccounts: []string{"ABCD1234", "EFGH5678"},
		Errors:          []SyncError{},
	}

	assert.Equal(t, 1, len(result.Accounts))
	assert.Equal(t, 2, result.AccountsRemoved)
	assert.Equal(t, 2, len(result.RemovedAccounts))
	assert.Contains(t, result.RemovedAccounts, "ABCD1234")
	assert.Contains(t, result.RemovedAccounts, "EFGH5678")
}

func TestSyncResult_WithErrors(t *testing.T) {
	result := &SyncResult{
		Accounts:        []string{"account1"},
		AccountsAdded:   0,
		AccountsRemoved: 0,
		AccountsUpdated: 1,
		RemovedAccounts: []string{},
		Errors: []SyncError{
			{
				AccountPublicKey: "ABCD1234",
				AccountName:      "failed-account",
				Error:            "connection timeout",
			},
			{
				AccountPublicKey: "",
				AccountName:      "",
				Error:            "failed to list resolver accounts: network error",
			},
		},
	}

	assert.Equal(t, 2, len(result.Errors))
	assert.Equal(t, "ABCD1234", result.Errors[0].AccountPublicKey)
	assert.Equal(t, "failed-account", result.Errors[0].AccountName)
	assert.Equal(t, "connection timeout", result.Errors[0].Error)
	assert.Equal(t, "failed to list resolver accounts: network error", result.Errors[1].Error)
}

func TestSyncError_AccountIdentification(t *testing.T) {
	tests := []struct {
		name       string
		syncError  SyncError
		hasAccount bool
	}{
		{
			name: "with account info",
			syncError: SyncError{
				AccountPublicKey: "ABCD1234",
				AccountName:      "test-account",
				Error:            "some error",
			},
			hasAccount: true,
		},
		{
			name: "without account info",
			syncError: SyncError{
				AccountPublicKey: "",
				AccountName:      "",
				Error:            "general error",
			},
			hasAccount: false,
		},
		{
			name: "only public key",
			syncError: SyncError{
				AccountPublicKey: "ABCD1234",
				AccountName:      "",
				Error:            "some error",
			},
			hasAccount: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasAccountInfo := tt.syncError.AccountPublicKey != "" || tt.syncError.AccountName != ""
			assert.Equal(t, tt.hasAccount, hasAccountInfo)
		})
	}
}
