package encryption

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/chacha20poly1305"
)

// generateTestKey generates a random 32-byte key and returns it base64-encoded
func generateTestKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, chacha20poly1305.KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(key)
}

func TestNewChaChaEncryptor(t *testing.T) {
	t.Run("success with single key", func(t *testing.T) {
		keys := map[string]string{
			"key-1": generateTestKey(t),
		}

		enc, err := NewChaChaEncryptor(keys, "key-1")
		require.NoError(t, err)
		require.NotNil(t, enc)
		assert.Equal(t, "key-1", enc.CurrentKeyID())
	})

	t.Run("success with multiple keys", func(t *testing.T) {
		keys := map[string]string{
			"key-1": generateTestKey(t),
			"key-2": generateTestKey(t),
			"key-3": generateTestKey(t),
		}

		enc, err := NewChaChaEncryptor(keys, "key-2")
		require.NoError(t, err)
		require.NotNil(t, enc)
		assert.Equal(t, "key-2", enc.CurrentKeyID())
	})

	t.Run("error with no keys", func(t *testing.T) {
		keys := map[string]string{}

		enc, err := NewChaChaEncryptor(keys, "")
		assert.Error(t, err)
		assert.Nil(t, enc)
		assert.Contains(t, err.Error(), "at least one encryption key is required")
	})

	t.Run("error with empty current key ID", func(t *testing.T) {
		keys := map[string]string{
			"key-1": generateTestKey(t),
		}

		enc, err := NewChaChaEncryptor(keys, "")
		assert.Error(t, err)
		assert.Nil(t, enc)
		assert.Contains(t, err.Error(), "current key ID is required")
	})

	t.Run("error with non-existent current key ID", func(t *testing.T) {
		keys := map[string]string{
			"key-1": generateTestKey(t),
		}

		enc, err := NewChaChaEncryptor(keys, "key-999")
		assert.Error(t, err)
		assert.Nil(t, enc)
		assert.Contains(t, err.Error(), "current key ID key-999 not found")
	})

	t.Run("error with invalid base64", func(t *testing.T) {
		keys := map[string]string{
			"key-1": "not-valid-base64!!!",
		}

		enc, err := NewChaChaEncryptor(keys, "key-1")
		assert.Error(t, err)
		assert.Nil(t, enc)
		assert.Contains(t, err.Error(), "failed to decode key")
	})

	t.Run("error with wrong key size", func(t *testing.T) {
		// 16 bytes instead of 32
		shortKey := make([]byte, 16)
		_, _ = rand.Read(shortKey)

		keys := map[string]string{
			"key-1": base64.StdEncoding.EncodeToString(shortKey),
		}

		enc, err := NewChaChaEncryptor(keys, "key-1")
		assert.Error(t, err)
		assert.Nil(t, enc)
		assert.Contains(t, err.Error(), "invalid size")
	})
}

func TestChaChaEncryptor_EncryptDecrypt(t *testing.T) {
	keys := map[string]string{
		"test-key": generateTestKey(t),
	}

	enc, err := NewChaChaEncryptor(keys, "test-key")
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("encrypt and decrypt round trip", func(t *testing.T) {
		plaintext := []byte("sensitive data that needs encryption")

		// Encrypt
		storageRef, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)
		assert.NotEmpty(t, storageRef)

		// Verify format
		assert.True(t, strings.HasPrefix(storageRef, "encrypted:test-key:"))

		// Decrypt
		decrypted, err := enc.Decrypt(ctx, storageRef)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("encrypt produces different ciphertext each time", func(t *testing.T) {
		plaintext := []byte("same plaintext")

		ref1, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)

		ref2, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)

		// Different ciphertexts due to random nonce
		assert.NotEqual(t, ref1, ref2)

		// But both decrypt to same plaintext
		dec1, err := enc.Decrypt(ctx, ref1)
		require.NoError(t, err)

		dec2, err := enc.Decrypt(ctx, ref2)
		require.NoError(t, err)

		assert.Equal(t, plaintext, dec1)
		assert.Equal(t, plaintext, dec2)
	})

	t.Run("encrypt empty data", func(t *testing.T) {
		plaintext := []byte{}

		storageRef, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)

		decrypted, err := enc.Decrypt(ctx, storageRef)
		require.NoError(t, err)
		// Empty byte slices may return as nil, which is equivalent
		assert.Empty(t, decrypted)
	})

	t.Run("encrypt large data", func(t *testing.T) {
		plaintext := make([]byte, 1024*1024) // 1MB
		_, _ = rand.Read(plaintext)

		storageRef, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)

		decrypted, err := enc.Decrypt(ctx, storageRef)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})
}

func TestChaChaEncryptor_Decrypt_Errors(t *testing.T) {
	keys := map[string]string{
		"test-key": generateTestKey(t),
	}

	enc, err := NewChaChaEncryptor(keys, "test-key")
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("invalid storage reference format", func(t *testing.T) {
		invalidRefs := []string{
			"",
			"invalid",
			"encrypted:",
			"encrypted:key",
			"only:two:parts",
		}

		for _, ref := range invalidRefs {
			_, err := enc.Decrypt(ctx, ref)
			assert.Error(t, err, "should fail for ref: %s", ref)
		}
	})

	t.Run("unsupported storage type", func(t *testing.T) {
		ref := "unsupported:key-id:data"
		_, err := enc.Decrypt(ctx, ref)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported storage type")
	})

	t.Run("vault storage not implemented", func(t *testing.T) {
		ref := "vault:secret/path:key"
		_, err := enc.Decrypt(ctx, ref)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vault storage not yet implemented")
	})

	t.Run("unknown key ID", func(t *testing.T) {
		ref := "encrypted:unknown-key:YWJjZGVm"
		_, err := enc.Decrypt(ctx, ref)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "encryption key unknown-key not found")
	})

	t.Run("invalid base64", func(t *testing.T) {
		ref := "encrypted:test-key:not-valid-base64!!!"
		_, err := enc.Decrypt(ctx, ref)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode ciphertext")
	})

	t.Run("ciphertext too short", func(t *testing.T) {
		ref := "encrypted:test-key:" + base64.StdEncoding.EncodeToString([]byte("short"))
		_, err := enc.Decrypt(ctx, ref)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ciphertext too short")
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		plaintext := []byte("original data")

		// Encrypt
		storageRef, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)

		// Tamper with the ciphertext
		parts := strings.Split(storageRef, ":")
		ciphertext, _ := base64.StdEncoding.DecodeString(parts[2])
		ciphertext[len(ciphertext)-1] ^= 0xFF // Flip bits in last byte
		tamperedRef := "encrypted:test-key:" + base64.StdEncoding.EncodeToString(ciphertext)

		// Should fail authentication
		_, err = enc.Decrypt(ctx, tamperedRef)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decrypt")
	})
}

func TestChaChaEncryptor_KeyRotation(t *testing.T) {
	ctx := context.Background()

	// Create encryptor with two keys
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)

	plaintext := []byte("data to rotate")

	// Create first encryptor that will encrypt with key-1
	enc1, err := NewChaChaEncryptor(map[string]string{
		"key-1": key1,
	}, "key-1")
	require.NoError(t, err)
	assert.Equal(t, "key-1", enc1.CurrentKeyID())

	// Encrypt with key-1
	ref1, err := enc1.Encrypt(ctx, plaintext)
	require.NoError(t, err)
	assert.Contains(t, ref1, "key-1")

	// Create new encryptor with both keys, key-2 is current
	// But we still have key-1 for decryption
	enc2, err := NewChaChaEncryptor(map[string]string{
		"key-2": key2,
		"key-1": key1,
	}, "key-2")
	require.NoError(t, err)

	t.Run("can decrypt old data with old key", func(t *testing.T) {
		decrypted, err := enc2.Decrypt(ctx, ref1)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("rotate to new key", func(t *testing.T) {
		ref2, err := enc2.RotateKey(ctx, ref1)
		require.NoError(t, err)
		assert.NotEqual(t, ref1, ref2)
		// The new reference should use the current key of enc2
		assert.Contains(t, ref2, enc2.CurrentKeyID())

		// Decrypt with new reference
		decrypted, err := enc2.Decrypt(ctx, ref2)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("rotate invalid reference", func(t *testing.T) {
		_, err := enc2.RotateKey(ctx, "invalid:ref:data")
		assert.Error(t, err)
	})
}

func TestChaChaEncryptor_MultipleKeys(t *testing.T) {
	ctx := context.Background()

	// Create encryptor with three keys
	keys := map[string]string{
		"key-2020": generateTestKey(t),
		"key-2021": generateTestKey(t),
		"key-2022": generateTestKey(t),
	}

	enc, err := NewChaChaEncryptor(keys, "key-2022")
	require.NoError(t, err)

	currentKey := enc.CurrentKeyID()
	assert.Equal(t, "key-2022", currentKey)

	t.Run("new encryptions use current key", func(t *testing.T) {
		plaintext := []byte("test data")

		ref, err := enc.Encrypt(ctx, plaintext)
		require.NoError(t, err)
		assert.Contains(t, ref, currentKey)
	})

	t.Run("can decrypt data encrypted with any key", func(t *testing.T) {
		plaintext := []byte("test data")

		// Manually create references as if encrypted with each key
		for keyID := range keys {
			// Create temp encryptor with this key as current
			tempEnc, err := NewChaChaEncryptor(map[string]string{keyID: keys[keyID]}, keyID)
			require.NoError(t, err)

			ref, err := tempEnc.Encrypt(ctx, plaintext)
			require.NoError(t, err)

			// Original encryptor should be able to decrypt
			decrypted, err := enc.Decrypt(ctx, ref)
			require.NoError(t, err)
			assert.Equal(t, plaintext, decrypted)
		}
	})
}

func TestChaChaEncryptor_CurrentKeyID(t *testing.T) {
	keys := map[string]string{
		"my-key": generateTestKey(t),
	}

	enc, err := NewChaChaEncryptor(keys, "my-key")
	require.NoError(t, err)

	assert.Equal(t, "my-key", enc.CurrentKeyID())
}
