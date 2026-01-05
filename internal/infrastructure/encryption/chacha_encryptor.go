package encryption

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// ChaChaEncryptor implements the Encryptor interface using ChaCha20-Poly1305
type ChaChaEncryptor struct {
	keys map[string][]byte // key ID -> key bytes
	currentKeyID string
}

// NewChaChaEncryptor creates a new ChaCha20-Poly1305 encryptor
// The currentKeyID specifies which key to use for new encryptions
// All keys are available for decryption (supports key rotation)
func NewChaChaEncryptor(keys map[string]string, currentKeyID string) (*ChaChaEncryptor, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one encryption key is required")
	}

	if currentKeyID == "" {
		return nil, fmt.Errorf("current key ID is required")
	}

	decodedKeys := make(map[string][]byte)

	for keyID, keyB64 := range keys {
		keyBytes, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode key %s: %w", keyID, err)
		}

		if len(keyBytes) != chacha20poly1305.KeySize {
			return nil, fmt.Errorf("key %s has invalid size: got %d, want %d", keyID, len(keyBytes), chacha20poly1305.KeySize)
		}

		decodedKeys[keyID] = keyBytes
	}

	// Verify that the current key ID exists
	if _, ok := decodedKeys[currentKeyID]; !ok {
		return nil, fmt.Errorf("current key ID %s not found in provided keys", currentKeyID)
	}

	return &ChaChaEncryptor{
		keys:         decodedKeys,
		currentKeyID: currentKeyID,
	}, nil
}

// Encrypt encrypts plaintext using ChaCha20-Poly1305 and returns a storage reference
func (e *ChaChaEncryptor) Encrypt(ctx context.Context, plaintext []byte) (string, error) {
	key, ok := e.keys[e.currentKeyID]
	if !ok {
		return "", fmt.Errorf("current key %s not found", e.currentKeyID)
	}

	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt: nonce + ciphertext (ciphertext includes auth tag)
	ciphertext := aead.Seal(nonce, nonce, plaintext, nil)

	// Encode to base64 and format as storage reference
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	storageRef := fmt.Sprintf("encrypted:%s:%s", e.currentKeyID, encoded)

	return storageRef, nil
}

// Decrypt decrypts a storage reference and returns the plaintext
func (e *ChaChaEncryptor) Decrypt(ctx context.Context, storageRef string) ([]byte, error) {
	// Parse storage reference format
	parts := strings.SplitN(storageRef, ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid storage reference format")
	}

	storageType := parts[0]
	keyID := parts[1]
	encodedData := parts[2]

	// Handle different storage types
	switch storageType {
	case "encrypted":
		// ChaCha20-Poly1305 encrypted data
		return e.decryptChaCha(keyID, encodedData)

	case "vault":
		// Future: Vault integration
		return nil, fmt.Errorf("vault storage not yet implemented")

	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}

// decryptChaCha decrypts ChaCha20-Poly1305 encrypted data
func (e *ChaChaEncryptor) decryptChaCha(keyID, encodedData string) ([]byte, error) {
	// Get the key for this key ID
	key, ok := e.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("encryption key %s not found", keyID)
	}

	// Decode base64
	ciphertext, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// Create cipher
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Verify minimum size (nonce + tag)
	nonceSize := aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and ciphertext
	nonce := ciphertext[:nonceSize]
	ciphertextOnly := ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := aead.Open(nil, nonce, ciphertextOnly, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// CurrentKeyID returns the current encryption key ID
func (e *ChaChaEncryptor) CurrentKeyID() string {
	return e.currentKeyID
}

// RotateKey re-encrypts data with the current key
func (e *ChaChaEncryptor) RotateKey(ctx context.Context, oldRef string) (string, error) {
	// Decrypt with old key
	plaintext, err := e.Decrypt(ctx, oldRef)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt during rotation: %w", err)
	}

	// Encrypt with current key
	newRef, err := e.Encrypt(ctx, plaintext)
	if err != nil {
		return "", fmt.Errorf("failed to re-encrypt during rotation: %w", err)
	}

	return newRef, nil
}
