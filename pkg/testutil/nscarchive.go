// Package testutil collects helpers shared between unit-test packages and the
// e2e suite. The split exists so tests can import a single canonical helper
// (e.g. buildMinimalNSCArchive) without each suite re-rolling its own.
package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"
)

// BuildMinimalNSCArchive constructs the smallest gzipped tarball that NIS's
// ImportFromNSC accepts: one operator JWT, one account JWT with one user, and
// the matching nkey seed files at the NSC store paths. Returns the archive
// as bytes ready for ImportFromNSC.
//
// The operatorName, accountName, and userName arguments are folded into both
// the JWT subject names and the directory layout, so the same archive can be
// re-used by tests that need distinct names per scenario.
//
// Used by:
//   - internal/application/services/partial_failure_test.go (rollback regression)
//   - tests/e2e/e2e_test.go                                  (Import_FromNSC e2e)
func BuildMinimalNSCArchive(t *testing.T, operatorName, accountName, userName string) []byte {
	t.Helper()

	// Operator key + JWT.
	opKP, err := nkeys.CreateOperator()
	require.NoError(t, err)
	opSeed, err := opKP.Seed()
	require.NoError(t, err)
	opPub, err := opKP.PublicKey()
	require.NoError(t, err)

	opClaims := jwt.NewOperatorClaims(opPub)
	opClaims.Name = operatorName
	opClaims.IssuedAt = time.Now().Unix()
	opJWT, err := opClaims.Encode(opKP)
	require.NoError(t, err)

	// Account key + JWT, signed by the operator.
	accKP, err := nkeys.CreateAccount()
	require.NoError(t, err)
	accSeed, err := accKP.Seed()
	require.NoError(t, err)
	accPub, err := accKP.PublicKey()
	require.NoError(t, err)

	accClaims := jwt.NewAccountClaims(accPub)
	accClaims.Name = accountName
	accClaims.IssuedAt = time.Now().Unix()
	accJWT, err := accClaims.Encode(opKP)
	require.NoError(t, err)

	// User key + JWT, signed by the account.
	userKP, err := nkeys.CreateUser()
	require.NoError(t, err)
	userSeed, err := userKP.Seed()
	require.NoError(t, err)
	userPub, err := userKP.PublicKey()
	require.NoError(t, err)

	userClaims := jwt.NewUserClaims(userPub)
	userClaims.Name = userName
	userClaims.IssuedAt = time.Now().Unix()
	userJWT, err := userClaims.Encode(accKP)
	require.NoError(t, err)

	files := map[string][]byte{
		filepath.Join("operator", "operator.jwt"):                                       []byte(opJWT),
		filepath.Join("operator", "accounts", accountName, accountName+".jwt"):           []byte(accJWT),
		filepath.Join("operator", "accounts", accountName, "users", userName+".jwt"):     []byte(userJWT),
		filepath.Join("nkeys", "keys", "O", opPub[1:3], opPub+".nk"):                     opSeed,
		filepath.Join("nkeys", "keys", "A", accPub[1:3], accPub+".nk"):                   accSeed,
		filepath.Join("nkeys", "keys", "U", userPub[1:3], userPub+".nk"):                 userSeed,
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, contents := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(contents)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(contents)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NotZero(t, buf.Len(), "empty archive generated")

	return buf.Bytes()
}

// ScopedSignerSpec describes a UserScope signing key that
// BuildNSCArchiveWithSigningKeys should attach to the synthetic
// account JWT. The role + template values are folded directly into the
// jwt.UserScope, then asserted by the e2e test on the import side so
// regressions in template-preserving logic surface as test failures.
type ScopedSignerSpec struct {
	Role     string
	PubAllow []string
	PubDeny  []string
	SubAllow []string
	SubDeny  []string
	RespMax  int
	RespTTL  time.Duration
}

// BuildNSCArchiveWithSigningKeys is BuildMinimalNSCArchive plus N extra
// signing keys on the account. plainCount is the number of raw-string
// signing keys (no UserScope) — used to exercise the "imported as
// plain signer" path. scoped is the slice of ScopedSignerSpec, one per
// UserScope to embed — used to exercise the template-preserving path.
//
// Returns the archive bytes plus the public keys of the signing keys
// it generated, in the order (plain..., scoped...), so callers can map
// each back to the SKK row they expect to find post-import.
func BuildNSCArchiveWithSigningKeys(t *testing.T, operatorName, accountName, userName string, plainCount int, scoped []ScopedSignerSpec) ([]byte, []string, []string) {
	t.Helper()

	opKP, err := nkeys.CreateOperator()
	require.NoError(t, err)
	opSeed, err := opKP.Seed()
	require.NoError(t, err)
	opPub, err := opKP.PublicKey()
	require.NoError(t, err)

	opClaims := jwt.NewOperatorClaims(opPub)
	opClaims.Name = operatorName
	opClaims.IssuedAt = time.Now().Unix()
	opJWT, err := opClaims.Encode(opKP)
	require.NoError(t, err)

	accKP, err := nkeys.CreateAccount()
	require.NoError(t, err)
	accSeed, err := accKP.Seed()
	require.NoError(t, err)
	accPub, err := accKP.PublicKey()
	require.NoError(t, err)

	accClaims := jwt.NewAccountClaims(accPub)
	accClaims.Name = accountName
	accClaims.IssuedAt = time.Now().Unix()

	// Generate signing keys. The plain ones are added via SigningKeys.Add
	// (stored as nil values in the map → emitted as raw strings in the
	// account JWT). The scoped ones are added via AddScopedSigner with a
	// populated UserScope.Template; the import path should preserve
	// these template fields verbatim on the resulting SKK row.
	files := map[string][]byte{}
	plainPubs := make([]string, 0, plainCount)
	for i := 0; i < plainCount; i++ {
		skKP, err := nkeys.CreateAccount()
		require.NoError(t, err)
		skSeed, err := skKP.Seed()
		require.NoError(t, err)
		skPub, err := skKP.PublicKey()
		require.NoError(t, err)
		plainPubs = append(plainPubs, skPub)
		accClaims.SigningKeys.Add(skPub)
		files[filepath.Join("nkeys", "keys", "A", skPub[1:3], skPub+".nk")] = skSeed
	}
	scopedPubs := make([]string, 0, len(scoped))
	for _, spec := range scoped {
		skKP, err := nkeys.CreateAccount()
		require.NoError(t, err)
		skSeed, err := skKP.Seed()
		require.NoError(t, err)
		skPub, err := skKP.PublicKey()
		require.NoError(t, err)
		scopedPubs = append(scopedPubs, skPub)

		us := jwt.NewUserScope()
		us.Key = skPub
		us.Role = spec.Role
		us.Template.Pub.Allow = append(jwt.StringList{}, spec.PubAllow...)
		us.Template.Pub.Deny = append(jwt.StringList{}, spec.PubDeny...)
		us.Template.Sub.Allow = append(jwt.StringList{}, spec.SubAllow...)
		us.Template.Sub.Deny = append(jwt.StringList{}, spec.SubDeny...)
		if spec.RespMax > 0 || spec.RespTTL > 0 {
			us.Template.Resp = &jwt.ResponsePermission{
				MaxMsgs: spec.RespMax,
				Expires: spec.RespTTL,
			}
		}
		accClaims.SigningKeys.AddScopedSigner(us)
		files[filepath.Join("nkeys", "keys", "A", skPub[1:3], skPub+".nk")] = skSeed
	}

	accJWT, err := accClaims.Encode(opKP)
	require.NoError(t, err)

	// User signed by the account main key — keeps the archive minimal.
	// Tests that need users-under-signers can add them on the NIS side
	// after import.
	userKP, err := nkeys.CreateUser()
	require.NoError(t, err)
	userSeed, err := userKP.Seed()
	require.NoError(t, err)
	userPub, err := userKP.PublicKey()
	require.NoError(t, err)

	userClaims := jwt.NewUserClaims(userPub)
	userClaims.Name = userName
	userClaims.IssuedAt = time.Now().Unix()
	userJWT, err := userClaims.Encode(accKP)
	require.NoError(t, err)

	files[filepath.Join("operator", "operator.jwt")] = []byte(opJWT)
	files[filepath.Join("operator", "accounts", accountName, accountName+".jwt")] = []byte(accJWT)
	files[filepath.Join("operator", "accounts", accountName, "users", userName+".jwt")] = []byte(userJWT)
	files[filepath.Join("nkeys", "keys", "O", opPub[1:3], opPub+".nk")] = opSeed
	files[filepath.Join("nkeys", "keys", "A", accPub[1:3], accPub+".nk")] = accSeed
	files[filepath.Join("nkeys", "keys", "U", userPub[1:3], userPub+".nk")] = userSeed

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, contents := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(contents)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(contents)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NotZero(t, buf.Len(), "empty archive generated")

	return buf.Bytes(), plainPubs, scopedPubs
}
