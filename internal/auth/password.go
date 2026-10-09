// Package auth hashes passwords with bcrypt and issues and verifies the signed access tokens (JWT)
// that staff members receive after logging in.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// MaxPasswordBytes is the longest password bcrypt can use. bcrypt ignores every byte after the 72nd, so without
// a limit a password would also match any longer password that starts with the same 72 bytes.
const MaxPasswordBytes = 72

// PasswordHasher hashes and checks passwords with bcrypt.
type PasswordHasher struct {
	cost int
}

// NewPasswordHasher returns a hasher that uses the given bcrypt cost.
// Production code passes bcrypt.DefaultCost; tests pass bcrypt.MinCost to stay fast.
func NewPasswordHasher(cost int) *PasswordHasher {
	return &PasswordHasher{cost: cost}
}

// Hash returns the bcrypt hash of password. It refuses passwords longer than MaxPasswordBytes,
// so callers validate the length before hashing.
func (h *PasswordHasher) Hash(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", fmt.Errorf("password hasher: hash: password is longer than %d bytes", MaxPasswordBytes)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("password hasher: hash: %w", err)
	}
	return string(hash), nil
}

// Compare reports whether password matches hash. A wrong password returns domain.ErrInvalidCredentials;
// anything else (for example a malformed stored hash) is a server-side problem and is returned wrapped,
// so it can never be mistaken for a user typing the wrong password.
//
// A password longer than MaxPasswordBytes never matches. No account can have one, and bcrypt would only compare
// its first 72 bytes, so "the real password plus anything" would otherwise be accepted. The answer does not depend
// on the hash, so rejecting early reveals nothing about which accounts exist.
func (h *PasswordHasher) Compare(hash, password string) error {
	if len(password) > MaxPasswordBytes {
		return domain.ErrInvalidCredentials
	}

	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return domain.ErrInvalidCredentials
	default:
		return fmt.Errorf("password hasher: compare: %w", err)
	}
}
