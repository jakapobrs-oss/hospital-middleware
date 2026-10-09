package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const (
	testSecret      = "unit-test-secret-with-at-least-32-characters"
	otherTestSecret = "a-completely-different-secret-for-tests"
	testTTL         = time.Hour
)

var (
	testIssueTime = time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)
	testIdentity  = domain.StaffIdentity{StaffID: 7, HospitalID: 2, HospitalCode: "hospital-a", Username: "nurse.somchai"}
)

// newTestTokenManager returns a manager whose clock is frozen at now.
func newTestTokenManager(secret string, now time.Time) *TokenManager {
	manager := NewTokenManager(secret, testTTL)
	manager.now = func() time.Time { return now }
	return manager
}

// validTestClaims returns the claims of a token that Verify accepts when the clock is at testIssueTime.
func validTestClaims() accessClaims {
	return accessClaims{
		HospitalID:   testIdentity.HospitalID,
		HospitalCode: testIdentity.HospitalCode,
		Username:     testIdentity.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   "7",
			IssuedAt:  jwt.NewNumericDate(testIssueTime),
			ExpiresAt: jwt.NewNumericDate(testIssueTime.Add(testTTL)),
		},
	}
}

func signTestToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.Claims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	require.NoError(t, err)
	return token
}

func TestTokenManager_Issue(t *testing.T) {
	manager := newTestTokenManager(testSecret, testIssueTime)

	t.Run("returns the moment the token expires", func(t *testing.T) {
		_, expiresAt, err := manager.Issue(testIdentity)

		require.NoError(t, err)
		assert.WithinDuration(t, testIssueTime.Add(testTTL), expiresAt, 0)
	})

	t.Run("signs with HS256 and writes the documented claims", func(t *testing.T) {
		token, _, err := manager.Issue(testIdentity)
		require.NoError(t, err)

		var claims accessClaims
		parsed, _, err := jwt.NewParser().ParseUnverified(token, &claims)

		require.NoError(t, err)
		assert.Equal(t, "HS256", parsed.Method.Alg())
		assert.Equal(t, "hospital-middleware", claims.Issuer)
		assert.Equal(t, "7", claims.Subject)
		assert.Equal(t, int64(2), claims.HospitalID)
		assert.Equal(t, "hospital-a", claims.HospitalCode)
		assert.Equal(t, "nurse.somchai", claims.Username)
		assert.Equal(t, testIssueTime.Unix(), claims.IssuedAt.Unix())
		assert.Equal(t, testIssueTime.Add(testTTL).Unix(), claims.ExpiresAt.Unix())
	})
}

func TestTokenManager_Verify_acceptsValidTokens(t *testing.T) {
	t.Run("returns the identity of a freshly issued token", func(t *testing.T) {
		manager := newTestTokenManager(testSecret, testIssueTime)
		token, _, err := manager.Issue(testIdentity)
		require.NoError(t, err)

		identity, err := manager.Verify(token)

		require.NoError(t, err)
		assert.Equal(t, testIdentity, identity)
	})

	t.Run("still accepts a token one second before it expires", func(t *testing.T) {
		issuer := newTestTokenManager(testSecret, testIssueTime)
		token, _, err := issuer.Issue(testIdentity)
		require.NoError(t, err)

		verifier := newTestTokenManager(testSecret, testIssueTime.Add(testTTL-time.Second))
		identity, err := verifier.Verify(token)

		require.NoError(t, err)
		assert.Equal(t, testIdentity, identity)
	})

	t.Run("works with the real clock", func(t *testing.T) {
		manager := NewTokenManager(testSecret, testTTL)
		token, _, err := manager.Issue(testIdentity)
		require.NoError(t, err)

		identity, err := manager.Verify(token)

		require.NoError(t, err)
		assert.Equal(t, testIdentity, identity)
	})
}

func TestTokenManager_Verify_rejectsInvalidTokens(t *testing.T) {
	verifier := newTestTokenManager(testSecret, testIssueTime)

	// signWithClaims signs a token with the right secret after letting the test break one claim.
	signWithClaims := func(mutate func(claims *accessClaims)) string {
		claims := validTestClaims()
		mutate(&claims)
		return signTestToken(t, jwt.SigningMethodHS256, []byte(testSecret), claims)
	}

	// A token whose payload was swapped for another hospital but kept the original signature.
	forgedClaims := validTestClaims()
	forgedClaims.HospitalID = 3
	forgedPayload, err := json.Marshal(forgedClaims)
	require.NoError(t, err)
	genuineParts := strings.Split(signTestToken(t, jwt.SigningMethodHS256, []byte(testSecret), validTestClaims()), ".")
	require.Len(t, genuineParts, 3)
	tamperedToken := strings.Join(
		[]string{genuineParts[0], base64.RawURLEncoding.EncodeToString(forgedPayload), genuineParts[2]}, ".")

	tests := []struct {
		name  string
		token string
	}{
		{name: "empty token", token: ""},
		{name: "not a jwt", token: "not-a-jwt"},
		{name: "payload changed after signing", token: tamperedToken},
		{
			name:  "signed with another secret",
			token: signTestToken(t, jwt.SigningMethodHS256, []byte(otherTestSecret), validTestClaims()),
		},
		{
			name:  "signed with another HMAC algorithm",
			token: signTestToken(t, jwt.SigningMethodHS384, []byte(testSecret), validTestClaims()),
		},
		{
			name:  "unsigned token using alg none",
			token: signTestToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validTestClaims()),
		},
		{
			name: "already expired",
			token: signWithClaims(func(claims *accessClaims) {
				claims.ExpiresAt = jwt.NewNumericDate(testIssueTime.Add(-time.Minute))
			}),
		},
		{
			name:  "no expiry claim",
			token: signWithClaims(func(claims *accessClaims) { claims.ExpiresAt = nil }),
		},
		{
			name:  "issued by someone else",
			token: signWithClaims(func(claims *accessClaims) { claims.Issuer = "someone-else" }),
		},
		{
			name:  "no issuer claim",
			token: signWithClaims(func(claims *accessClaims) { claims.Issuer = "" }),
		},
		{
			name:  "subject is not a number",
			token: signWithClaims(func(claims *accessClaims) { claims.Subject = "seven" }),
		},
		{
			name:  "subject is zero",
			token: signWithClaims(func(claims *accessClaims) { claims.Subject = "0" }),
		},
		{
			name:  "no hospital id",
			token: signWithClaims(func(claims *accessClaims) { claims.HospitalID = 0 }),
		},
		{
			name:  "no hospital code",
			token: signWithClaims(func(claims *accessClaims) { claims.HospitalCode = "" }),
		},
		{
			name:  "no username",
			token: signWithClaims(func(claims *accessClaims) { claims.Username = "" }),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := verifier.Verify(tc.token)

			assert.ErrorIs(t, err, domain.ErrInvalidToken)
			assert.Zero(t, identity)
		})
	}

	t.Run("a token is rejected once its lifetime has passed", func(t *testing.T) {
		issuer := newTestTokenManager(testSecret, testIssueTime)
		token, _, err := issuer.Issue(testIdentity)
		require.NoError(t, err)

		lateVerifier := newTestTokenManager(testSecret, testIssueTime.Add(testTTL+time.Second))
		_, err = lateVerifier.Verify(token)

		assert.ErrorIs(t, err, domain.ErrInvalidToken)
	})
}
