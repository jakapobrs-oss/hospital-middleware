package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const (
	// tokenIssuer is written to the "iss" claim and required again when a token is verified.
	tokenIssuer = "hospital-middleware"

	// signingMethodName is the only algorithm accepted on verification. Pinning it blocks
	// "alg: none" tokens and algorithm-confusion attacks.
	signingMethodName = "HS256"
)

// accessClaims are the claims inside an access token. The staff id travels in the registered "sub" claim.
type accessClaims struct {
	HospitalID   int64  `json:"hospital_id"`
	HospitalCode string `json:"hospital_code"`
	Username     string `json:"username"`
	jwt.RegisteredClaims
}

// TokenManager issues and verifies HS256-signed access tokens.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time // replaced in tests to control issue and expiry times
}

// NewTokenManager returns a manager that signs tokens with secret and lets them live for ttl.
func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), ttl: ttl, now: time.Now}
}

// Issue signs a token for identity and returns it together with the moment it expires.
// The "exp" claim has one-second precision, so the token can lapse up to a second before expiresAt.
func (m *TokenManager) Issue(identity domain.StaffIdentity) (token string, expiresAt time.Time, err error) {
	issuedAt := m.now().UTC()
	expiresAt = issuedAt.Add(m.ttl)

	claims := accessClaims{
		HospitalID:   identity.HospitalID,
		HospitalCode: identity.HospitalCode,
		Username:     identity.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   strconv.FormatInt(identity.StaffID, 10),
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token manager: sign token: %w", err)
	}
	return token, expiresAt, nil
}

// Verify checks the signature, algorithm, issuer and expiry of token and returns the staff identity inside it.
// Every failure wraps domain.ErrInvalidToken, so callers can answer with one generic 401.
func (m *TokenManager) Verify(token string) (domain.StaffIdentity, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{signingMethodName}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(m.now),
	)

	var claims accessClaims
	if _, err := parser.ParseWithClaims(token, &claims, m.signingKey); err != nil {
		return domain.StaffIdentity{}, fmt.Errorf("%w: %v", domain.ErrInvalidToken, err)
	}

	// A token we signed always carries these claims; anything else is treated as forged or from an older format.
	staffID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || staffID <= 0 || claims.HospitalID <= 0 || claims.HospitalCode == "" || claims.Username == "" {
		return domain.StaffIdentity{}, fmt.Errorf("%w: required claims are missing", domain.ErrInvalidToken)
	}

	return domain.StaffIdentity{
		StaffID:      staffID,
		HospitalID:   claims.HospitalID,
		HospitalCode: claims.HospitalCode,
		Username:     claims.Username,
	}, nil
}

// signingKey is the jwt.Keyfunc: every token is checked against the one shared secret.
func (m *TokenManager) signingKey(*jwt.Token) (any, error) {
	return m.secret, nil
}
