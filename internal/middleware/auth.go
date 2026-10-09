package middleware

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const (
	bearerScheme = "Bearer"

	// One message for every kind of failure, so a client cannot tell a missing header
	// from a malformed, forged or expired token.
	unauthorizedMessage = "missing or invalid bearer token"

	asciiBlanks = " \t"
)

// bearerTokenPattern is the RFC 6750 b64token syntax: 1*( ALPHA / DIGIT / "-" / "." / "_" / "~" / "+" / "/" ) *"=".
var bearerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)

// TokenVerifier checks an access token and returns the staff member it was issued to.
type TokenVerifier interface {
	Verify(token string) (domain.StaffIdentity, error)
}

// RequireAuth lets a request through only when it carries "Authorization: Bearer <token>" and the token verifies.
// The authenticated staff member is stored on the context for StaffIdentityFrom; every other request gets 401.
func RequireAuth(verifier TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, found := bearerTokenFrom(c.GetHeader("Authorization"))
		if !found {
			rejectUnauthorized(c)
			return
		}

		identity, err := verifier.Verify(token)
		if err != nil {
			rejectUnauthorized(c)
			return
		}

		SetStaffIdentity(c, identity)
		c.Next()
	}
}

// bearerTokenFrom extracts the token from an Authorization header value. The scheme is case-insensitive
// (RFC 7235); an empty token or any other scheme counts as not found. Only ASCII blanks separate the parts
// and the token must use the RFC 6750 b64token alphabet: Nginx keys its rate limit on the token text, so a
// variant such as a no-break space before the token must fail here rather than count as a different token.
func bearerTokenFrom(headerValue string) (token string, found bool) {
	scheme, credentials, hasCredentials := strings.Cut(strings.Trim(headerValue, asciiBlanks), " ")
	token = strings.Trim(credentials, asciiBlanks)
	if !hasCredentials || !strings.EqualFold(scheme, bearerScheme) || !bearerTokenPattern.MatchString(token) {
		return "", false
	}
	return token, true
}

func rejectUnauthorized(c *gin.Context) {
	// RFC 6750 asks a 401 from a bearer-protected resource to name the scheme it expects.
	c.Header("WWW-Authenticate", bearerScheme)
	apierror.Respond(c, http.StatusUnauthorized, apierror.CodeUnauthorized, unauthorizedMessage)
}
