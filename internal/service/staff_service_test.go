package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/jakapobrs-oss/hospital-middleware/internal/auth"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const (
	validPassword      = "S3cure-Passw0rd"
	storedPasswordHash = "hashed:" + validPassword // what the fake hasher produces for validPassword
)

var (
	hospitalA = domain.Hospital{ID: 1, Code: "hospital-a", Name: "Hospital A"}
	hospitalB = domain.Hospital{ID: 2, Code: "hospital-b", Name: "Hospital B"}

	staffCreatedAt = time.Date(2026, 10, 10, 3, 15, 0, 0, time.UTC)
	tokenExpiresAt = time.Date(2026, 10, 10, 4, 15, 0, 0, time.UTC)
)

// staffLookup records one GetByHospitalAndUsername call.
type staffLookup struct {
	hospitalID int64
	username   string
}

// passwordCheck records one PasswordHasher.Compare call.
type passwordCheck struct {
	hash     string
	password string
}

// fakeHospitalLookup is a hand-written HospitalRepository; each test can replace findByCodeOrName.
type fakeHospitalLookup struct {
	findByCodeOrName func(ctx context.Context, reference string) (domain.Hospital, error)
	references       []string // references passed to FindByCodeOrName, in call order
}

func (f *fakeHospitalLookup) FindByCodeOrName(ctx context.Context, reference string) (domain.Hospital, error) {
	f.references = append(f.references, reference)
	return f.findByCodeOrName(ctx, reference)
}

// fakeStaffStore is a hand-written StaffRepository; each test can replace its funcs.
type fakeStaffStore struct {
	create                   func(ctx context.Context, staff domain.Staff) (domain.Staff, error)
	getByHospitalAndUsername func(ctx context.Context, hospitalID int64, username string) (domain.Staff, error)
	created                  []domain.Staff // accounts passed to Create
	lookups                  []staffLookup  // arguments of GetByHospitalAndUsername calls
}

func (f *fakeStaffStore) Create(ctx context.Context, staff domain.Staff) (domain.Staff, error) {
	f.created = append(f.created, staff)
	return f.create(ctx, staff)
}

func (f *fakeStaffStore) GetByHospitalAndUsername(ctx context.Context, hospitalID int64, username string) (domain.Staff, error) {
	f.lookups = append(f.lookups, staffLookup{hospitalID: hospitalID, username: username})
	return f.getByHospitalAndUsername(ctx, hospitalID, username)
}

// fakePasswordHasher is a hand-written PasswordHasher; each test can replace hash and compare.
type fakePasswordHasher struct {
	hash    func(password string) (string, error)
	compare func(hash, password string) error
	hashed  []string        // passwords passed to Hash
	checks  []passwordCheck // arguments of Compare calls
}

func (f *fakePasswordHasher) Hash(password string) (string, error) {
	f.hashed = append(f.hashed, password)
	return f.hash(password)
}

func (f *fakePasswordHasher) Compare(hash, password string) error {
	f.checks = append(f.checks, passwordCheck{hash: hash, password: password})
	return f.compare(hash, password)
}

// fakeTokenIssuer is a hand-written TokenIssuer; each test can replace issue.
type fakeTokenIssuer struct {
	issue  func(identity domain.StaffIdentity) (string, time.Time, error)
	issued []domain.StaffIdentity // identities passed to Issue
}

func (f *fakeTokenIssuer) Issue(identity domain.StaffIdentity) (string, time.Time, error) {
	f.issued = append(f.issued, identity)
	return f.issue(identity)
}

// staffServiceFixture is a StaffService wired to fakes that behave like a happy database:
// hospital-a and hospital-b exist, and nurse01 of hospital-a has the password validPassword.
// Registration is open, like a service created without a registration key.
type staffServiceFixture struct {
	hospitals *fakeHospitalLookup
	staff     *fakeStaffStore
	hasher    *fakePasswordHasher
	tokens    *fakeTokenIssuer
	service   *StaffService
}

func newStaffServiceFixture() *staffServiceFixture {
	fixture := &staffServiceFixture{
		hospitals: &fakeHospitalLookup{
			// Stands in for the repository: a reference matches a hospital by code or by lower-case name.
			findByCodeOrName: func(_ context.Context, reference string) (domain.Hospital, error) {
				for _, hospital := range []domain.Hospital{hospitalA, hospitalB} {
					if reference == hospital.Code || reference == strings.ToLower(hospital.Name) {
						return hospital, nil
					}
				}
				return domain.Hospital{}, domain.ErrHospitalNotFound
			},
		},
		staff: &fakeStaffStore{
			create: func(_ context.Context, staff domain.Staff) (domain.Staff, error) {
				staff.ID = 42
				staff.CreatedAt = staffCreatedAt
				return staff, nil
			},
			getByHospitalAndUsername: func(_ context.Context, hospitalID int64, username string) (domain.Staff, error) {
				if hospitalID == hospitalA.ID && username == "nurse01" {
					return domain.Staff{
						ID:           42,
						HospitalID:   hospitalA.ID,
						Username:     "nurse01",
						PasswordHash: storedPasswordHash,
						CreatedAt:    staffCreatedAt,
					}, nil
				}
				return domain.Staff{}, domain.ErrStaffNotFound
			},
		},
		hasher: &fakePasswordHasher{
			hash: func(password string) (string, error) { return "hashed:" + password, nil },
			compare: func(hash, password string) error {
				if hash == "hashed:"+password {
					return nil
				}
				return domain.ErrInvalidCredentials
			},
		},
		tokens: &fakeTokenIssuer{
			issue: func(identity domain.StaffIdentity) (string, time.Time, error) {
				return "token-for-" + identity.Username, tokenExpiresAt, nil
			},
		},
	}
	fixture.service = NewStaffService(fixture.hospitals, fixture.staff, fixture.hasher, fixture.tokens, "")
	return fixture
}

// newProtectedStaffServiceFixture is the same service created with a registration key.
func newProtectedStaffServiceFixture(registrationKey string) *staffServiceFixture {
	fixture := newStaffServiceFixture()
	fixture.service = NewStaffService(fixture.hospitals, fixture.staff, fixture.hasher, fixture.tokens, registrationKey)
	return fixture
}

func TestStaffService_Create(t *testing.T) {
	ctx := context.Background()
	input := CreateStaffInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-a"}

	t.Run("registers the account and returns it with its hospital", func(t *testing.T) {
		fixture := newStaffServiceFixture()

		result, err := fixture.service.Create(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, hospitalA, result.Hospital)
		assert.Equal(t, int64(42), result.Staff.ID)
		assert.Equal(t, int64(1), result.Staff.HospitalID)
		assert.Equal(t, "nurse01", result.Staff.Username)
		assert.Equal(t, staffCreatedAt, result.Staff.CreatedAt)
	})

	t.Run("stores only the password hash and never returns it", func(t *testing.T) {
		fixture := newStaffServiceFixture()

		result, err := fixture.service.Create(ctx, input)

		require.NoError(t, err)
		require.Len(t, fixture.staff.created, 1)
		assert.Equal(t, storedPasswordHash, fixture.staff.created[0].PasswordHash)
		assert.NotEqual(t, validPassword, fixture.staff.created[0].PasswordHash, "the plain password must not be stored")
		assert.Empty(t, result.Staff.PasswordHash, "the hash must not travel back to the handler")
	})

	t.Run("normalises the username and hospital reference but not the password", func(t *testing.T) {
		fixture := newStaffServiceFixture()
		messyInput := CreateStaffInput{Username: "  Nurse01  ", Password: "  S3cure Passw0rd  ", HospitalCode: " Hospital-A "}

		_, err := fixture.service.Create(ctx, messyInput)

		require.NoError(t, err)
		assert.Equal(t, []string{"hospital-a"}, fixture.hospitals.references)
		assert.Equal(t, []string{"  S3cure Passw0rd  "}, fixture.hasher.hashed)
		require.Len(t, fixture.staff.created, 1)
		assert.Equal(t, "nurse01", fixture.staff.created[0].Username)
	})

	t.Run("accepts the hospital by name", func(t *testing.T) {
		fixture := newStaffServiceFixture()
		byName := CreateStaffInput{Username: "nurse01", Password: validPassword, HospitalCode: "Hospital A"}

		result, err := fixture.service.Create(ctx, byName)

		require.NoError(t, err)
		assert.Equal(t, hospitalA, result.Hospital)
		assert.Equal(t, []string{"hospital a"}, fixture.hospitals.references, "the repository gets the lower-cased reference")
		require.Len(t, fixture.staff.created, 1)
		assert.Equal(t, hospitalA.ID, fixture.staff.created[0].HospitalID)
	})

	t.Run("returns ErrHospitalNotFound for an unknown hospital without touching passwords or accounts", func(t *testing.T) {
		fixture := newStaffServiceFixture()

		_, err := fixture.service.Create(ctx, CreateStaffInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-z"})

		assert.ErrorIs(t, err, domain.ErrHospitalNotFound)
		assert.Empty(t, fixture.hasher.hashed)
		assert.Empty(t, fixture.staff.created)
	})

	t.Run("returns ErrUsernameTaken when the repository reports a duplicate", func(t *testing.T) {
		fixture := newStaffServiceFixture()
		fixture.staff.create = func(context.Context, domain.Staff) (domain.Staff, error) {
			return domain.Staff{}, domain.ErrUsernameTaken
		}

		result, err := fixture.service.Create(ctx, input)

		assert.ErrorIs(t, err, domain.ErrUsernameTaken)
		assert.Equal(t, CreatedStaff{}, result)
	})

	t.Run("reports server-side failures without mapping them to a domain error", func(t *testing.T) {
		tests := []struct {
			name          string
			failure       error
			injectFailure func(fixture *staffServiceFixture, failure error)
		}{
			{
				name:    "hospital lookup fails",
				failure: errors.New("database is down"),
				injectFailure: func(fixture *staffServiceFixture, failure error) {
					fixture.hospitals.findByCodeOrName = func(context.Context, string) (domain.Hospital, error) {
						return domain.Hospital{}, failure
					}
				},
			},
			{
				name:    "password hashing fails",
				failure: errors.New("bcrypt exploded"),
				injectFailure: func(fixture *staffServiceFixture, failure error) {
					fixture.hasher.hash = func(string) (string, error) { return "", failure }
				},
			},
			{
				name:    "saving the account fails",
				failure: errors.New("insert failed"),
				injectFailure: func(fixture *staffServiceFixture, failure error) {
					fixture.staff.create = func(context.Context, domain.Staff) (domain.Staff, error) {
						return domain.Staff{}, failure
					}
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fixture := newStaffServiceFixture()
				tc.injectFailure(fixture, tc.failure)

				_, err := fixture.service.Create(ctx, input)

				assert.ErrorIs(t, err, tc.failure)
				assert.NotErrorIs(t, err, domain.ErrHospitalNotFound)
				assert.NotErrorIs(t, err, domain.ErrUsernameTaken)
				assert.NotErrorIs(t, err, domain.ErrInvalidRegistrationKey)
			})
		}
	})
}

func TestStaffService_Create_registrationKey(t *testing.T) {
	ctx := context.Background()
	const registrationKey = "let-me-in-2026"
	input := CreateStaffInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-a"}

	t.Run("registration stays open when no key is configured", func(t *testing.T) {
		for _, provided := range []string{"", "a key nobody asked for"} {
			fixture := newStaffServiceFixture()
			withKey := input
			withKey.RegistrationKey = provided

			_, err := fixture.service.Create(ctx, withKey)

			assert.NoError(t, err, "provided key %q", provided)
		}
	})

	rejected := []struct {
		name     string
		provided string
	}{
		{name: "no key sent", provided: ""},
		{name: "wrong key", provided: "wrong-key"},
		{name: "start of the real key", provided: registrationKey[:5]},
		{name: "real key with extra characters", provided: registrationKey + "x"},
		{name: "real key in another letter case", provided: strings.ToUpper(registrationKey)},
		{name: "same length, different content", provided: strings.Repeat("x", len(registrationKey))},
		{name: "real key with surrounding spaces", provided: " " + registrationKey + " "},
	}
	for _, tc := range rejected {
		t.Run("rejects: "+tc.name, func(t *testing.T) {
			fixture := newProtectedStaffServiceFixture(registrationKey)
			withKey := input
			withKey.RegistrationKey = tc.provided

			result, err := fixture.service.Create(ctx, withKey)

			assert.Equal(t, domain.ErrInvalidRegistrationKey, err, "always the same bare error")
			assert.Equal(t, CreatedStaff{}, result)
			assert.Empty(t, fixture.hospitals.references, "no hospital lookup before the key is checked")
			assert.Empty(t, fixture.hasher.hashed, "no password hashing before the key is checked")
			assert.Empty(t, fixture.staff.created, "nothing may be stored")
		})
	}

	t.Run("accepts the right key", func(t *testing.T) {
		fixture := newProtectedStaffServiceFixture(registrationKey)
		withKey := input
		withKey.RegistrationKey = registrationKey

		result, err := fixture.service.Create(ctx, withKey)

		require.NoError(t, err)
		assert.Equal(t, "nurse01", result.Staff.Username)
		assert.Len(t, fixture.staff.created, 1)
	})

	t.Run("login does not need the key", func(t *testing.T) {
		fixture := newProtectedStaffServiceFixture(registrationKey)

		result, err := fixture.service.Login(ctx, LoginInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-a"})

		require.NoError(t, err)
		assert.Equal(t, "token-for-nurse01", result.AccessToken)
	})
}

func TestStaffService_Login(t *testing.T) {
	ctx := context.Background()
	input := LoginInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-a"}

	t.Run("returns a token, the account and its hospital", func(t *testing.T) {
		fixture := newStaffServiceFixture()

		result, err := fixture.service.Login(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "token-for-nurse01", result.AccessToken)
		assert.Equal(t, tokenExpiresAt, result.ExpiresAt)
		assert.Equal(t, hospitalA, result.Hospital)
		assert.Equal(t, int64(42), result.Staff.ID)
		assert.Equal(t, "nurse01", result.Staff.Username)
		assert.Empty(t, result.Staff.PasswordHash, "the hash must not travel back to the handler")
	})

	t.Run("puts the staff member and hospital into the token", func(t *testing.T) {
		fixture := newStaffServiceFixture()

		_, err := fixture.service.Login(ctx, input)

		require.NoError(t, err)
		wantIdentity := domain.StaffIdentity{StaffID: 42, HospitalID: 1, HospitalCode: "hospital-a", Username: "nurse01"}
		assert.Equal(t, []domain.StaffIdentity{wantIdentity}, fixture.tokens.issued)
	})

	t.Run("checks the password against the stored hash exactly once", func(t *testing.T) {
		fixture := newStaffServiceFixture()

		_, err := fixture.service.Login(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, []passwordCheck{{hash: storedPasswordHash, password: validPassword}}, fixture.hasher.checks)
	})

	t.Run("normalises the username and hospital reference", func(t *testing.T) {
		fixture := newStaffServiceFixture()
		messyInput := LoginInput{Username: "  NURSE01 ", Password: validPassword, HospitalCode: " Hospital-A  "}

		_, err := fixture.service.Login(ctx, messyInput)

		require.NoError(t, err)
		assert.Equal(t, []string{"hospital-a"}, fixture.hospitals.references)
		assert.Equal(t, []staffLookup{{hospitalID: 1, username: "nurse01"}}, fixture.staff.lookups)
	})

	t.Run("accepts the hospital by name and puts its code into the token", func(t *testing.T) {
		fixture := newStaffServiceFixture()
		byName := LoginInput{Username: "nurse01", Password: validPassword, HospitalCode: "Hospital A"}

		result, err := fixture.service.Login(ctx, byName)

		require.NoError(t, err)
		assert.Equal(t, hospitalA, result.Hospital)
		require.Len(t, fixture.tokens.issued, 1)
		assert.Equal(t, "hospital-a", fixture.tokens.issued[0].HospitalCode, "the token carries the canonical code, not what was typed")
	})
}

func TestStaffService_Login_rejectsBadCredentials(t *testing.T) {
	tests := []struct {
		name         string
		input        LoginInput
		wantHashUsed string // the hash the submitted password must have been checked against
	}{
		{
			name:         "unknown hospital",
			input:        LoginInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-z"},
			wantHashUsed: dummyPasswordHash,
		},
		{
			name:         "unknown username",
			input:        LoginInput{Username: "ghost", Password: validPassword, HospitalCode: "hospital-a"},
			wantHashUsed: dummyPasswordHash,
		},
		{
			name:         "username that only exists in another hospital",
			input:        LoginInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-b"},
			wantHashUsed: dummyPasswordHash,
		},
		{
			name:         "wrong password",
			input:        LoginInput{Username: "nurse01", Password: "wrong-password", HospitalCode: "hospital-a"},
			wantHashUsed: storedPasswordHash,
		},
		{
			name:         "password that only differs by surrounding spaces",
			input:        LoginInput{Username: "nurse01", Password: " " + validPassword + " ", HospitalCode: "hospital-a"},
			wantHashUsed: storedPasswordHash,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newStaffServiceFixture()

			result, err := fixture.service.Login(context.Background(), tc.input)

			assert.Equal(t, domain.ErrInvalidCredentials, err, "every failure must be the same bare error")
			assert.Equal(t, LoginResult{}, result)
			assert.Empty(t, fixture.tokens.issued, "no token may be issued")
			// One bcrypt check in every case, so a failed login takes equally long whatever was wrong.
			assert.Equal(t, []passwordCheck{{hash: tc.wantHashUsed, password: tc.input.Password}}, fixture.hasher.checks)
		})
	}
}

// bcrypt reads only the first 72 bytes of a password. This test runs the real hasher through the service to prove
// that an account registered with a 72 byte password cannot be entered with "that password plus anything".
func TestStaffService_Login_withRealBcrypt_rejectsPasswordsLongerThanTheLimit(t *testing.T) {
	ctx := context.Background()
	fixture := newStaffServiceFixture()
	storedAccounts := map[string]domain.Staff{}
	fixture.staff.create = func(_ context.Context, staff domain.Staff) (domain.Staff, error) {
		staff.ID = 42
		storedAccounts[staff.Username] = staff
		return staff, nil
	}
	fixture.staff.getByHospitalAndUsername = func(_ context.Context, hospitalID int64, username string) (domain.Staff, error) {
		if staff, found := storedAccounts[username]; found && staff.HospitalID == hospitalID {
			return staff, nil
		}
		return domain.Staff{}, domain.ErrStaffNotFound
	}
	service := NewStaffService(fixture.hospitals, fixture.staff, auth.NewPasswordHasher(bcrypt.MinCost), fixture.tokens, "")

	longestPassword := strings.Repeat("a", auth.MaxPasswordBytes)
	_, err := service.Create(ctx, CreateStaffInput{Username: "nurse01", Password: longestPassword, HospitalCode: "hospital-a"})
	require.NoError(t, err)

	tests := []struct {
		name     string
		username string
		password string
		wantErr  error // nil means the login must succeed
	}{
		{name: "the registered password logs in", username: "nurse01", password: longestPassword},
		{name: "the registered password plus one byte", username: "nurse01", password: longestPassword + "x", wantErr: domain.ErrInvalidCredentials},
		{name: "the registered password plus many bytes", username: "nurse01", password: longestPassword + strings.Repeat("z", 200), wantErr: domain.ErrInvalidCredentials},
		{name: "a long password for an unknown user", username: "ghost", password: longestPassword + "x", wantErr: domain.ErrInvalidCredentials},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := service.Login(ctx, LoginInput{Username: tc.username, Password: tc.password, HospitalCode: "hospital-a"})

			if tc.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, "token-for-nurse01", result.AccessToken)
				return
			}
			assert.Equal(t, tc.wantErr, err)
			assert.Equal(t, LoginResult{}, result)
		})
	}

	t.Run("registering a password over the limit fails instead of silently truncating it", func(t *testing.T) {
		_, err := service.Create(ctx, CreateStaffInput{Username: "nurse02", Password: longestPassword + "x", HospitalCode: "hospital-a"})

		assert.Error(t, err)
		assert.NotContains(t, storedAccounts, "nurse02")
	})
}

func TestStaffService_Login_reportsServerFailuresAsErrors(t *testing.T) {
	tests := []struct {
		name          string
		failure       error
		injectFailure func(fixture *staffServiceFixture, failure error)
	}{
		{
			name:    "hospital lookup fails",
			failure: errors.New("database is down"),
			injectFailure: func(fixture *staffServiceFixture, failure error) {
				fixture.hospitals.findByCodeOrName = func(context.Context, string) (domain.Hospital, error) {
					return domain.Hospital{}, failure
				}
			},
		},
		{
			name:    "staff lookup fails",
			failure: errors.New("query timed out"),
			injectFailure: func(fixture *staffServiceFixture, failure error) {
				fixture.staff.getByHospitalAndUsername = func(context.Context, int64, string) (domain.Staff, error) {
					return domain.Staff{}, failure
				}
			},
		},
		{
			name:    "stored hash is malformed",
			failure: errors.New("hashedSecret too short to be a bcrypted password"),
			injectFailure: func(fixture *staffServiceFixture, failure error) {
				fixture.hasher.compare = func(string, string) error { return failure }
			},
		},
		{
			name:    "token signing fails",
			failure: errors.New("signer unavailable"),
			injectFailure: func(fixture *staffServiceFixture, failure error) {
				fixture.tokens.issue = func(domain.StaffIdentity) (string, time.Time, error) {
					return "", time.Time{}, failure
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newStaffServiceFixture()
			tc.injectFailure(fixture, tc.failure)

			result, err := fixture.service.Login(context.Background(),
				LoginInput{Username: "nurse01", Password: validPassword, HospitalCode: "hospital-a"})

			assert.ErrorIs(t, err, tc.failure)
			assert.NotErrorIs(t, err, domain.ErrInvalidCredentials, "an outage must not look like a wrong password")
			assert.Equal(t, LoginResult{}, result)
		})
	}
}

// The dummy hash only equalises login timing if bcrypt really runs on it. Login ignores the comparison result,
// so a malformed hash would fail instantly and silently bring the user-enumeration leak back.
func TestDummyPasswordHash_isValidBcryptAtProductionCost(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(dummyPasswordHash))
	require.NoError(t, err)
	assert.Equal(t, bcrypt.DefaultCost, cost)

	err = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte("any-password"))

	assert.ErrorIs(t, err, bcrypt.ErrMismatchedHashAndPassword, "a well-formed hash reports a plain mismatch, not a parse error")
}

func TestStaffService_DatabaseCallsHaveADeadline(t *testing.T) {
	fixture := newStaffServiceFixture()
	var deadlineSeen []bool
	wrappedLookup := fixture.hospitals.findByCodeOrName
	fixture.hospitals.findByCodeOrName = func(ctx context.Context, reference string) (domain.Hospital, error) {
		_, hasDeadline := ctx.Deadline()
		deadlineSeen = append(deadlineSeen, hasDeadline)
		return wrappedLookup(ctx, reference)
	}

	_, _ = fixture.service.Create(context.Background(), CreateStaffInput{Username: "nurse99", Password: "S3cure-Passw0rd", HospitalCode: "hospital-a"})
	_, _ = fixture.service.Login(context.Background(), LoginInput{Username: "nurse01", Password: "S3cure-Passw0rd", HospitalCode: "hospital-a"})

	require.Len(t, deadlineSeen, 2)
	assert.Equal(t, []bool{true, true}, deadlineSeen, "a locked table must not hang create or login")
}
