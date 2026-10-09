package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const testPassword = "S3cure-Passw0rd"

func TestPasswordHasher_Hash(t *testing.T) {
	hasher := NewPasswordHasher(bcrypt.MinCost)

	t.Run("returns a bcrypt hash that is not the plain password", func(t *testing.T) {
		hash, err := hasher.Hash(testPassword)

		require.NoError(t, err)
		assert.NotEqual(t, testPassword, hash)
		assert.True(t, strings.HasPrefix(hash, "$2"), "bcrypt hashes start with $2")
	})

	t.Run("uses the configured cost", func(t *testing.T) {
		hash, err := hasher.Hash(testPassword)
		require.NoError(t, err)

		cost, err := bcrypt.Cost([]byte(hash))

		require.NoError(t, err)
		assert.Equal(t, bcrypt.MinCost, cost)
	})

	t.Run("salts every hash so equal passwords hash differently", func(t *testing.T) {
		first, err := hasher.Hash(testPassword)
		require.NoError(t, err)
		second, err := hasher.Hash(testPassword)
		require.NoError(t, err)

		assert.NotEqual(t, first, second)
	})

	t.Run("rejects a password longer than the 72 byte bcrypt limit", func(t *testing.T) {
		_, err := hasher.Hash(strings.Repeat("a", 73))

		assert.Error(t, err)
	})

	t.Run("reports a cost bcrypt cannot use as an error", func(t *testing.T) {
		_, err := NewPasswordHasher(bcrypt.MaxCost + 1).Hash(testPassword)

		assert.Error(t, err)
	})
}

func TestPasswordHasher_Compare(t *testing.T) {
	hasher := NewPasswordHasher(bcrypt.MinCost)
	hash, err := hasher.Hash(testPassword)
	require.NoError(t, err)

	t.Run("accepts the original password", func(t *testing.T) {
		assert.NoError(t, hasher.Compare(hash, testPassword))
	})

	wrongPasswords := []struct {
		name     string
		password string
	}{
		{name: "different letter case", password: "s3cure-passw0rd"},
		{name: "trailing space", password: testPassword + " "},
		{name: "empty password", password: ""},
		{name: "unrelated password", password: "completely-different"},
	}
	for _, tc := range wrongPasswords {
		t.Run("rejects a wrong password: "+tc.name, func(t *testing.T) {
			err := hasher.Compare(hash, tc.password)

			assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
		})
	}

	t.Run("reports a malformed stored hash as an internal error, not as a wrong password", func(t *testing.T) {
		err := hasher.Compare("not-a-bcrypt-hash", testPassword)

		require.Error(t, err)
		assert.NotErrorIs(t, err, domain.ErrInvalidCredentials)
	})
}

// bcrypt reads only the first 72 bytes of a password, so without a guard "the real password plus anything"
// would log in. These tests run the real bcrypt to prove the guard closes that hole.
func TestPasswordHasher_Compare_rejectsPasswordsLongerThanTheBcryptLimit(t *testing.T) {
	hasher := NewPasswordHasher(bcrypt.MinCost)

	tests := []struct {
		name     string
		stored   string // the password the account was registered with: exactly MaxPasswordBytes bytes
		attempts []string
	}{
		{
			name:     "ascii password",
			stored:   strings.Repeat("a", MaxPasswordBytes),
			attempts: []string{strings.Repeat("a", MaxPasswordBytes) + "x", strings.Repeat("a", MaxPasswordBytes+50)},
		},
		{
			name:     "thai password of 24 characters (72 bytes)",
			stored:   strings.Repeat("ก", MaxPasswordBytes/3),
			attempts: []string{strings.Repeat("ก", MaxPasswordBytes/3+1), strings.Repeat("ก", MaxPasswordBytes/3) + "!"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Len(t, tc.stored, MaxPasswordBytes, "test setup: the stored password must sit exactly on the limit")
			hash, err := hasher.Hash(tc.stored)
			require.NoError(t, err)

			assert.NoError(t, hasher.Compare(hash, tc.stored), "the password itself still works")
			for _, attempt := range tc.attempts {
				assert.ErrorIs(t, hasher.Compare(hash, attempt), domain.ErrInvalidCredentials,
					"%d byte attempt must not match", len(attempt))
			}
		})
	}

	t.Run("a long attempt is rejected whatever the stored hash is", func(t *testing.T) {
		err := hasher.Compare("not-even-a-hash", strings.Repeat("a", MaxPasswordBytes+1))

		assert.ErrorIs(t, err, domain.ErrInvalidCredentials, "the answer must not depend on the account")
	})

	t.Run("Hash refuses a password over the limit with a clear error", func(t *testing.T) {
		_, err := hasher.Hash(strings.Repeat("a", MaxPasswordBytes+1))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "longer than 72 bytes")
	})
}
