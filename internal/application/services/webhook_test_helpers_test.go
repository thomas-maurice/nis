package services

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

// Shared helpers used by webhook_service_test.go and the webhook job-handler
// test. Lifted from the pre-A16 webhook_delivery_worker_test.go.
//
// webhookTestDB itself lives in webhook_service_test.go — kept there so this
// helpers file remains zero-dependency on goose/migrations setup.

func workerTestEncryptor(t *testing.T) encryption.Encryptor {
	t.Helper()
	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)
	return enc
}
