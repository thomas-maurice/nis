package services

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseAgeRecipient_X25519 verifies that the canonical age1... wire form
// round-trips through our dispatcher.
func TestParseAgeRecipient_X25519(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	pub := id.Recipient().String()

	r, err := parseAgeRecipient(pub)
	require.NoError(t, err)
	require.Equal(t, pub, r.(*age.X25519Recipient).String())
}

// TestParseAgeRecipient_Malformed asserts that random garbage rejects with
// ErrInvalidAgeRecipient — handler maps this to CodeInvalidArgument.
func TestParseAgeRecipient_Malformed(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"not-an-age-key",
		"age1invalid",
		"age1xyzabc", // valid prefix, garbage payload
		"ssh-rsa not-a-real-key",
		"random-string-that-isnt-age-shaped",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := parseAgeRecipient(tc)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidAgeRecipient,
				"want ErrInvalidAgeRecipient, got %v", err)
		})
	}
}

// TestEncryptForBackup_RoundTrip — encrypt with one recipient, decrypt with
// the matching identity, recover the exact plaintext. This is the canonical
// proof that the encrypt step doesn't corrupt the YAML payload.
func TestEncryptForBackup_RoundTrip(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)

	plaintext := []byte("apiVersion: nis/v1\nkind: Operator\nmetadata:\n  name: test\n")

	ciphertext, err := encryptForBackup(plaintext, []age.Recipient{id.Recipient()})
	require.NoError(t, err)

	// Sanity: ciphertext is age-formatted (header check).
	require.True(t, bytes.HasPrefix(ciphertext, []byte("age-encryption.org/v1\n")),
		"ciphertext does not start with age header")

	// Round-trip.
	r, err := age.Decrypt(bytes.NewReader(ciphertext), id)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// TestEncryptForBackup_MultiRecipient_EitherDecrypts — encrypt to two
// recipients; either identity should decrypt independently. This is the
// canonical multi-recipient property age provides.
func TestEncryptForBackup_MultiRecipient_EitherDecrypts(t *testing.T) {
	id1, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	id2, err := age.GenerateX25519Identity()
	require.NoError(t, err)

	plaintext := []byte("operator-tree-yaml")

	ciphertext, err := encryptForBackup(plaintext, []age.Recipient{id1.Recipient(), id2.Recipient()})
	require.NoError(t, err)

	// Decrypt with id1.
	r1, err := age.Decrypt(bytes.NewReader(ciphertext), id1)
	require.NoError(t, err)
	out1, err := io.ReadAll(r1)
	require.NoError(t, err)
	assert.Equal(t, plaintext, out1)

	// Decrypt with id2 — independently.
	r2, err := age.Decrypt(bytes.NewReader(ciphertext), id2)
	require.NoError(t, err)
	out2, err := io.ReadAll(r2)
	require.NoError(t, err)
	assert.Equal(t, plaintext, out2)
}

// TestEncryptForBackup_NoRecipients — defensive guard. The mainline path
// (RunBackup) checks ErrNoBackupRecipients earlier; this pins the helper's
// own refusal so a future call site can't accidentally encrypt to nobody.
func TestEncryptForBackup_NoRecipients(t *testing.T) {
	_, err := encryptForBackup([]byte("anything"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoBackupRecipients)
}

// TestEncryptForBackup_WrongIdentityFails — sanity that decrypt with the
// wrong identity errors. Without this, a buggy implementation that
// accidentally produces all-recipients-can-decrypt would pass the
// multi-recipient test.
func TestEncryptForBackup_WrongIdentityFails(t *testing.T) {
	id1, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	id2, err := age.GenerateX25519Identity()
	require.NoError(t, err)

	ciphertext, err := encryptForBackup([]byte("secret"), []age.Recipient{id1.Recipient()})
	require.NoError(t, err)

	_, err = age.Decrypt(bytes.NewReader(ciphertext), id2)
	require.Error(t, err)
	// The age package returns its own error type here; we don't depend on
	// the exact string but assert it's not a nil-error / success path.
	assert.True(t, errors.Is(err, err), "want non-nil decrypt error, got %v", err)
	// Message contains "no identity matched" or "incorrect identity";
	// loose match keeps the test resilient across age versions.
	low := strings.ToLower(err.Error())
	assert.Contains(t, low, "identity", "decrypt error should mention identity matching: %s", err)
}
