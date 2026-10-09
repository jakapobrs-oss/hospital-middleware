package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// dummyPasswordHash is a well-formed bcrypt hash (cost 10, the production cost) of a password nobody knows.
// Login checks the submitted password against it when the hospital or username does not exist, so a failed
// login takes as long as a real one and response time does not reveal which accounts exist.
const dummyPasswordHash = "$2a$10$Vh3kQ9yTn2LsPzXw7eRb1uGm6Jd0aWcYfNt4xBq8HiZ5oKlUe2vSr"

// HospitalRepository finds the hospital a person named.
type HospitalRepository interface {
	// FindByCodeOrName accepts a hospital code or name as typed by a person. It returns
	// domain.ErrHospitalNotFound when no single hospital matches.
	FindByCodeOrName(ctx context.Context, reference string) (domain.Hospital, error)
}

// StaffRepository stores and reads staff accounts.
type StaffRepository interface {
	Create(ctx context.Context, staff domain.Staff) (domain.Staff, error)
	GetByHospitalAndUsername(ctx context.Context, hospitalID int64, username string) (domain.Staff, error)
}

// PasswordHasher hashes passwords and checks them against a stored hash.
// Compare returns domain.ErrInvalidCredentials when the password does not match.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) error
}

// TokenIssuer signs an access token for an authenticated staff member.
type TokenIssuer interface {
	Issue(identity domain.StaffIdentity) (string, time.Time, error)
}

// CreateStaffInput is the data needed to register a staff account.
type CreateStaffInput struct {
	Username string
	Password string
	// HospitalCode is the hospital's code or name as typed by the caller, for example "hospital-a" or "Hospital A".
	HospitalCode string
	// RegistrationKey is what the caller sent in the X-Registration-Key header. It is only checked when the
	// service was created with a registration key.
	RegistrationKey string
}

// CreatedStaff is a newly registered account together with its hospital.
type CreatedStaff struct {
	Staff    domain.Staff
	Hospital domain.Hospital
}

// LoginInput is the data a staff member submits to log in.
type LoginInput struct {
	Username string
	Password string
	// HospitalCode is the hospital's code or name as typed by the caller.
	HospitalCode string
}

// LoginResult is what a successful login returns.
type LoginResult struct {
	AccessToken string
	ExpiresAt   time.Time
	Staff       domain.Staff
	Hospital    domain.Hospital
}

// StaffService registers staff accounts and logs staff in.
type StaffService struct {
	hospitals       HospitalRepository
	staff           StaffRepository
	hasher          PasswordHasher
	tokens          TokenIssuer
	registrationKey string
}

// NewStaffService wires the staff use cases to their collaborators.
// An empty registrationKey leaves staff registration open, as the assignment describes. A non-empty one protects
// it: Create then only accepts requests that carry the same key.
func NewStaffService(hospitals HospitalRepository, staff StaffRepository, hasher PasswordHasher, tokens TokenIssuer, registrationKey string) *StaffService {
	return &StaffService{hospitals: hospitals, staff: staff, hasher: hasher, tokens: tokens, registrationKey: registrationKey}
}

// Create registers a staff account in the hospital named by input.HospitalCode.
// It returns domain.ErrInvalidRegistrationKey when registration is protected and the key is wrong,
// domain.ErrHospitalNotFound for an unknown hospital and domain.ErrUsernameTaken when the username already exists
// in that hospital. The password hash never leaves this method.
func (s *StaffService) Create(ctx context.Context, input CreateStaffInput) (CreatedStaff, error) {
	// The key is checked first, before any lookup or the expensive password hash, so a caller without it costs nothing.
	if !s.registrationKeyAccepted(input.RegistrationKey) {
		return CreatedStaff{}, domain.ErrInvalidRegistrationKey
	}
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()

	hospital, err := s.hospitals.FindByCodeOrName(ctx, normaliseIdentifier(input.HospitalCode))
	if err != nil {
		return CreatedStaff{}, fmt.Errorf("staff service: create: find hospital: %w", err)
	}

	passwordHash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return CreatedStaff{}, fmt.Errorf("staff service: create: hash password: %w", err)
	}

	created, err := s.staff.Create(ctx, domain.Staff{
		HospitalID:   hospital.ID,
		Username:     normaliseIdentifier(input.Username),
		PasswordHash: passwordHash,
	})
	if err != nil {
		return CreatedStaff{}, fmt.Errorf("staff service: create: save staff: %w", err)
	}

	created.PasswordHash = ""
	return CreatedStaff{Staff: created, Hospital: hospital}, nil
}

// Login checks the credentials and returns an access token for the staff member.
// Every authentication failure (unknown hospital, unknown username, wrong password) returns the same
// domain.ErrInvalidCredentials; any other error is a server-side failure.
func (s *StaffService) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()

	hospital, staff, err := s.findAccount(ctx, normaliseIdentifier(input.HospitalCode), normaliseIdentifier(input.Username))
	if errors.Is(err, domain.ErrHospitalNotFound) || errors.Is(err, domain.ErrStaffNotFound) {
		// Spend the same bcrypt time as a real check. The outcome is ignored on purpose: no account matched.
		_ = s.hasher.Compare(dummyPasswordHash, input.Password)
		return LoginResult{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}

	err = s.hasher.Compare(staff.PasswordHash, input.Password)
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		return LoginResult{}, domain.ErrInvalidCredentials
	case err != nil:
		return LoginResult{}, fmt.Errorf("staff service: login: check password: %w", err)
	}

	accessToken, expiresAt, err := s.tokens.Issue(domain.StaffIdentity{
		StaffID:      staff.ID,
		HospitalID:   hospital.ID,
		HospitalCode: hospital.Code,
		Username:     staff.Username,
	})
	if err != nil {
		return LoginResult{}, fmt.Errorf("staff service: login: issue token: %w", err)
	}

	staff.PasswordHash = ""
	return LoginResult{AccessToken: accessToken, ExpiresAt: expiresAt, Staff: staff, Hospital: hospital}, nil
}

// findAccount looks up the hospital and then the staff member inside it.
// The returned errors wrap domain.ErrHospitalNotFound or domain.ErrStaffNotFound when nothing matched.
func (s *StaffService) findAccount(ctx context.Context, hospitalReference, username string) (domain.Hospital, domain.Staff, error) {
	hospital, err := s.hospitals.FindByCodeOrName(ctx, hospitalReference)
	if err != nil {
		return domain.Hospital{}, domain.Staff{}, fmt.Errorf("staff service: login: find hospital: %w", err)
	}

	staff, err := s.staff.GetByHospitalAndUsername(ctx, hospital.ID, username)
	if err != nil {
		return domain.Hospital{}, domain.Staff{}, fmt.Errorf("staff service: login: find staff: %w", err)
	}
	return hospital, staff, nil
}

// registrationKeyAccepted reports whether a Create request may proceed. Registration without a configured key is open.
// Otherwise the keys are compared as SHA-256 digests in constant time, so neither the content nor the length of the
// real key can be probed through response times.
func (s *StaffService) registrationKeyAccepted(provided string) bool {
	if s.registrationKey == "" {
		return true
	}
	expectedDigest := sha256.Sum256([]byte(s.registrationKey))
	providedDigest := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(expectedDigest[:], providedDigest[:]) == 1
}

// normaliseIdentifier trims surrounding spaces and lowercases a username or hospital reference, the form the
// database stores. Passwords are never normalised.
func normaliseIdentifier(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
