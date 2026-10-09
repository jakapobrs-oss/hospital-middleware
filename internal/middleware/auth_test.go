package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const unauthorizedBody = `{"error":{"code":"unauthorized","message":"missing or invalid bearer token"}}`

var authTestIdentity = domain.StaffIdentity{StaffID: 7, HospitalID: 2, HospitalCode: "hospital-a", Username: "nurse.somchai"}

func init() {
	gin.SetMode(gin.TestMode)
}

// fakeTokenVerifier answers every Verify call with the configured result and records the tokens it was asked about.
type fakeTokenVerifier struct {
	identity       domain.StaffIdentity
	err            error
	receivedTokens []string
}

func (f *fakeTokenVerifier) Verify(token string) (domain.StaffIdentity, error) {
	f.receivedTokens = append(f.receivedTokens, token)
	return f.identity, f.err
}

// protectedRoute is the result of calling the protected test route.
type protectedRoute struct {
	response       *httptest.ResponseRecorder
	handlerCalled  bool
	identityOnCtx  domain.StaffIdentity
	identityWasSet bool
}

// callProtectedRoute sends GET /protected through RequireAuth with the given Authorization header ("" = none).
func callProtectedRoute(verifier TokenVerifier, authorizationHeader string) protectedRoute {
	result := protectedRoute{response: httptest.NewRecorder()}

	router := gin.New()
	router.GET("/protected", RequireAuth(verifier), func(c *gin.Context) {
		result.handlerCalled = true
		result.identityOnCtx, result.identityWasSet = StaffIdentityFrom(c)
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authorizationHeader != "" {
		request.Header.Set("Authorization", authorizationHeader)
	}
	router.ServeHTTP(result.response, request)
	return result
}

func TestRequireAuth_allowsValidToken(t *testing.T) {
	tests := []struct {
		name                string
		authorizationHeader string
	}{
		{name: "canonical scheme", authorizationHeader: "Bearer the-token"},
		{name: "lower case scheme", authorizationHeader: "bearer the-token"},
		{name: "upper case scheme", authorizationHeader: "BEARER the-token"},
		{name: "extra spaces around the token", authorizationHeader: "Bearer   the-token  "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &fakeTokenVerifier{identity: authTestIdentity}

			result := callProtectedRoute(verifier, tc.authorizationHeader)

			assert.Equal(t, http.StatusNoContent, result.response.Code)
			assert.True(t, result.handlerCalled)
			assert.Equal(t, []string{"the-token"}, verifier.receivedTokens)
		})
	}

	t.Run("stores the verified identity for the handler", func(t *testing.T) {
		verifier := &fakeTokenVerifier{identity: authTestIdentity}

		result := callProtectedRoute(verifier, "Bearer the-token")

		require.True(t, result.identityWasSet)
		assert.Equal(t, authTestIdentity, result.identityOnCtx)
	})
}

func TestRequireAuth_rejectsRequest(t *testing.T) {
	tests := []struct {
		name                string
		authorizationHeader string
		verifierErr         error
		wantVerifyCalls     int
	}{
		{name: "no Authorization header", authorizationHeader: "", wantVerifyCalls: 0},
		{name: "other scheme", authorizationHeader: "Basic dXNlcjpwYXNz", wantVerifyCalls: 0},
		{name: "scheme only", authorizationHeader: "Bearer", wantVerifyCalls: 0},
		{name: "scheme followed by blanks", authorizationHeader: "Bearer    ", wantVerifyCalls: 0},
		{name: "bare token without scheme", authorizationHeader: "the-token", wantVerifyCalls: 0},
		{
			name:                "token rejected by the verifier",
			authorizationHeader: "Bearer the-token",
			verifierErr:         domain.ErrInvalidToken,
			wantVerifyCalls:     1,
		},
		{
			name:                "unexpected verifier error",
			authorizationHeader: "Bearer the-token",
			verifierErr:         errors.New("verifier exploded"),
			wantVerifyCalls:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &fakeTokenVerifier{identity: authTestIdentity, err: tc.verifierErr}

			result := callProtectedRoute(verifier, tc.authorizationHeader)

			assert.Equal(t, http.StatusUnauthorized, result.response.Code)
			assert.JSONEq(t, unauthorizedBody, result.response.Body.String(), "every failure must look the same")
			assert.Equal(t, "Bearer", result.response.Header().Get("WWW-Authenticate"))
			assert.False(t, result.handlerCalled, "the protected handler must not run")
			assert.Len(t, verifier.receivedTokens, tc.wantVerifyCalls)
		})
	}
}

func TestStaffIdentityFrom_withoutRequireAuth(t *testing.T) {
	router := gin.New()
	var identityWasSet bool
	router.GET("/open", func(c *gin.Context) {
		_, identityWasSet = StaffIdentityFrom(c)
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/open", nil))

	assert.False(t, identityWasSet, "an unprotected route has no staff identity")
}
