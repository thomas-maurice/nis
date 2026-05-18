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
