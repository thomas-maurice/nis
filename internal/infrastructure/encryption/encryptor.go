package encryption

import "context"

// Encryptor defines the interface for encrypting and decrypting sensitive data
type Encryptor interface {
	// Encrypt encrypts plaintext and returns a storage reference
	// Format: "encrypted:<key_id>:<base64_ciphertext>"
	Encrypt(ctx context.Context, plaintext []byte) (string, error)

	// Decrypt decrypts a storage reference and returns the plaintext
	// Supports formats:
	// - "encrypted:<key_id>:<base64_ciphertext>"
	// - Future: "vault:secret/path/to/key"
	Decrypt(ctx context.Context, storageRef string) ([]byte, error)

	// CurrentKeyID returns the current encryption key ID
	// This is the key that will be used for new encryptions
	CurrentKeyID() string

	// RotateKey re-encrypts data with the current key
	// Takes an old storage reference and returns a new one
	RotateKey(ctx context.Context, oldRef string) (string, error)
}
