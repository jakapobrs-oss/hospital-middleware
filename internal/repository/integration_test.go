//go:build integration

// Integration tests against a real PostgreSQL. They need a disposable database:
//
//	TEST_DATABASE_URL=postgres://postgres:test@localhost:55432/postgres?sslmode=disable \
//	  go test -tags integration ./internal/repository/...
//
// (`make test-integration` starts a throwaway PostgreSQL container and runs them.)
package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/repository"
)

type testDatabase struct {
	pool      *pgxpool.Pool
	hospitalA domain.Hospital
	hospitalB domain.Hospital
}

// setupDatabase migrates the database, empties the staff and patient tables and returns the seeded hospitals.
func setupDatabase(t *testing.T) testDatabase {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()

	require.NoError(t, repository.RunMigrations(databaseURL))
	require.NoError(t, repository.RunMigrations(databaseURL), "migrations must be idempotent")

	pool, err := repository.NewPostgresPool(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, "TRUNCATE patients, staff RESTART IDENTITY")
	require.NoError(t, err)

	hospitals := repository.NewHospitalRepository(pool)
	hospitalA, err := hospitals.GetByCode(ctx, "hospital-a")
	require.NoError(t, err)
	hospitalB, err := hospitals.GetByCode(ctx, "hospital-b")
	require.NoError(t, err)
	return testDatabase{pool: pool, hospitalA: hospitalA, hospitalB: hospitalB}
}

func dateOf(year int, month time.Month, day int) *time.Time {
	value := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &value
}

func TestIntegration_HospitalRepository_GetByCode(t *testing.T) {
	database := setupDatabase(t)
	hospitals := repository.NewHospitalRepository(database.pool)

	assert.Equal(t, "Hospital A", database.hospitalA.Name)

	_, err := hospitals.GetByCode(context.Background(), "hospital-z")
	assert.ErrorIs(t, err, domain.ErrHospitalNotFound)
}

func TestIntegration_StaffRepository_UsernameIsUniquePerHospital(t *testing.T) {
	database := setupDatabase(t)
	staffRepository := repository.NewStaffRepository(database.pool)
	ctx := context.Background()

	created, err := staffRepository.Create(ctx, domain.Staff{HospitalID: database.hospitalA.ID, Username: "nurse01", PasswordHash: "hash-a"})
	require.NoError(t, err)
	assert.Positive(t, created.ID)
	assert.False(t, created.CreatedAt.IsZero())

	_, err = staffRepository.Create(ctx, domain.Staff{HospitalID: database.hospitalA.ID, Username: "nurse01", PasswordHash: "hash-x"})
	assert.ErrorIs(t, err, domain.ErrUsernameTaken)

	_, err = staffRepository.Create(ctx, domain.Staff{HospitalID: database.hospitalB.ID, Username: "nurse01", PasswordHash: "hash-b"})
	assert.NoError(t, err, "the same username may exist in another hospital")

	found, err := staffRepository.GetByHospitalAndUsername(ctx, database.hospitalB.ID, "nurse01")
	require.NoError(t, err)
	assert.Equal(t, "hash-b", found.PasswordHash)

	_, err = staffRepository.GetByHospitalAndUsername(ctx, database.hospitalB.ID, "nobody")
	assert.ErrorIs(t, err, domain.ErrStaffNotFound)
}

func TestIntegration_PatientRepository_UpsertInsertsThenRefreshes(t *testing.T) {
	database := setupDatabase(t)
	patients := repository.NewPatientRepository(database.pool)
	ctx := context.Background()

	original := domain.Patient{
		HospitalID: database.hospitalA.ID, PatientHN: "HN-1", NationalID: "1100000000016",
		FirstNameTH: "สมชาย", LastNameTH: "ใจดี", FirstNameEN: "Somchai", LastNameEN: "Jaidee",
		DateOfBirth: dateOf(1985, time.April, 12), PhoneNumber: "080-000-0001", Gender: domain.GenderMale,
	}
	require.NoError(t, patients.UpsertFromHIS(ctx, original))

	refreshed := original
	refreshed.PhoneNumber = "080-000-9999"
	refreshed.Email = "somchai.new@example.com"
	require.NoError(t, patients.UpsertFromHIS(ctx, refreshed))

	found, total, err := patients.Search(ctx, database.hospitalA.ID, domain.PatientSearchCriteria{NationalID: "1100000000016", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total, "same hospital + HN must update, not duplicate")
	assert.Equal(t, "080-000-9999", found[0].PhoneNumber)
	assert.Equal(t, "somchai.new@example.com", found[0].Email)
	assert.Equal(t, "", found[0].PassportID, "NULL columns come back as empty strings")
	assert.Equal(t, "", found[0].MiddleNameTH)
	require.NotNil(t, found[0].DateOfBirth)
	assert.Equal(t, "1985-04-12", found[0].DateOfBirth.Format("2006-01-02"))
	assert.True(t, found[0].UpdatedAt.After(found[0].CreatedAt) || found[0].UpdatedAt.Equal(found[0].CreatedAt))
}

func TestIntegration_PatientRepository_HISWinsWhenAnIdentityMovesToANewHN(t *testing.T) {
	database := setupDatabase(t)
	patients := repository.NewPatientRepository(database.pool)
	ctx := context.Background()

	require.NoError(t, patients.UpsertFromHIS(ctx, domain.Patient{
		HospitalID: database.hospitalA.ID, PatientHN: "HN-OLD", NationalID: "1100000000016", PhoneNumber: "080-000-0001",
	}))
	// The HIS now reports the same national ID under a new HN (e.g. after merging two registrations).
	require.NoError(t, patients.UpsertFromHIS(ctx, domain.Patient{
		HospitalID: database.hospitalA.ID, PatientHN: "HN-NEW", NationalID: "1100000000016", PhoneNumber: "089-999-9999",
	}))

	found, total, err := patients.Search(ctx, database.hospitalA.ID, domain.PatientSearchCriteria{NationalID: "1100000000016", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, "HN-NEW", found[0].PatientHN)
	assert.Equal(t, "089-999-9999", found[0].PhoneNumber)
}

func TestIntegration_PatientRepository_StoresLongHISValues(t *testing.T) {
	database := setupDatabase(t)
	patients := repository.NewPatientRepository(database.pool)

	err := patients.UpsertFromHIS(context.Background(), domain.Patient{
		HospitalID: database.hospitalA.ID, PatientHN: "HN-2026-0000000000000001", PassportID: "AB123456789012345678901",
		PhoneNumber: "+66 (0) 80-000-0001 ext. 1234",
	})

	assert.NoError(t, err, "values from an HIS are not truncated or rejected for their length")
}

func TestIntegration_PatientRepository_RejectsPatientWithoutIdentityNumber(t *testing.T) {
	database := setupDatabase(t)
	patients := repository.NewPatientRepository(database.pool)

	err := patients.UpsertFromHIS(context.Background(), domain.Patient{HospitalID: database.hospitalA.ID, PatientHN: "HN-X"})

	assert.Error(t, err)
}

func seedSearchFixtures(t *testing.T, database testDatabase) *repository.PatientRepository {
	t.Helper()
	patients := repository.NewPatientRepository(database.pool)
	fixtures := []domain.Patient{
		{HospitalID: database.hospitalA.ID, PatientHN: "A-1", NationalID: "1100000000016",
			FirstNameTH: "สมชาย", LastNameTH: "ใจดี", FirstNameEN: "Somchai", LastNameEN: "Jaidee",
			DateOfBirth: dateOf(1985, time.April, 12), PhoneNumber: "080-000-0001", Email: "Somchai.J@Example.com"},
		{HospitalID: database.hospitalA.ID, PatientHN: "A-2", NationalID: "1100000000024",
			FirstNameTH: "สมหญิง", MiddleNameTH: "มณี", LastNameTH: "รักไทย",
			FirstNameEN: "Somying", MiddleNameEN: "Manee", LastNameEN: "Rakthai_100%"},
		{HospitalID: database.hospitalA.ID, PatientHN: "A-3", PassportID: "AA1234567",
			FirstNameEN: "John", LastNameEN: "Smith"},
		// Same person as A-1, registered at Hospital B.
		{HospitalID: database.hospitalB.ID, PatientHN: "B-1", NationalID: "1100000000016",
			FirstNameTH: "สมชาย", LastNameTH: "ใจดี", FirstNameEN: "Somchai", LastNameEN: "Jaidee"},
	}
	for _, fixture := range fixtures {
		require.NoError(t, patients.UpsertFromHIS(context.Background(), fixture))
	}
	return patients
}

func TestIntegration_PatientRepository_Search(t *testing.T) {
	database := setupDatabase(t)
	patients := seedSearchFixtures(t, database)

	testCases := []struct {
		name        string
		hospitalID  int64
		criteria    domain.PatientSearchCriteria
		expectedHNs []string
	}{
		{name: "no filter returns the whole hospital", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{}, expectedHNs: []string{"A-1", "A-2", "A-3"}},
		{name: "hospital B never sees hospital A rows", hospitalID: database.hospitalB.ID,
			criteria: domain.PatientSearchCriteria{NationalID: "1100000000016"}, expectedHNs: []string{"B-1"}},
		{name: "partial English first name, any case", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{FirstName: "SOM"}, expectedHNs: []string{"A-1", "A-2"}},
		{name: "partial Thai last name", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{LastName: "รัก"}, expectedHNs: []string{"A-2"}},
		{name: "middle name in either language", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{MiddleName: "มณ"}, expectedHNs: []string{"A-2"}},
		{name: "LIKE wildcards are matched literally", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{LastName: "_100%"}, expectedHNs: []string{"A-2"}},
		{name: "a lone % does not match everything", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{FirstName: "%"}, expectedHNs: nil},
		{name: "passport is case-insensitive", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{PassportID: "aa1234567"}, expectedHNs: []string{"A-3"}},
		{name: "phone matches on digits only", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{PhoneNumber: "0800000001"}, expectedHNs: []string{"A-1"}},
		{name: "phone in international form", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{PhoneNumber: "+66 80 000 0001"}, expectedHNs: []string{"A-1"}},
		{name: "email is case-insensitive", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{Email: "somchai.j@example.com"}, expectedHNs: []string{"A-1"}},
		{name: "date of birth", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{DateOfBirth: dateOf(1985, time.April, 12)}, expectedHNs: []string{"A-1"}},
		{name: "filters combine with AND", hospitalID: database.hospitalA.ID,
			criteria: domain.PatientSearchCriteria{FirstName: "som", LastName: "smith"}, expectedHNs: nil},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			criteria := testCase.criteria
			criteria.Limit = 20

			found, total, err := patients.Search(context.Background(), testCase.hospitalID, criteria)

			require.NoError(t, err)
			var foundHNs []string
			for _, patient := range found {
				foundHNs = append(foundHNs, patient.PatientHN)
				assert.Equal(t, testCase.hospitalID, patient.HospitalID)
			}
			assert.Equal(t, testCase.expectedHNs, foundHNs)
			assert.Equal(t, len(testCase.expectedHNs), total)
		})
	}
}

func TestIntegration_PatientRepository_SearchPaginates(t *testing.T) {
	database := setupDatabase(t)
	patients := seedSearchFixtures(t, database)

	firstPage, total, err := patients.Search(context.Background(), database.hospitalA.ID, domain.PatientSearchCriteria{Limit: 2, Offset: 0})
	require.NoError(t, err)
	secondPage, _, err := patients.Search(context.Background(), database.hospitalA.ID, domain.PatientSearchCriteria{Limit: 2, Offset: 2})
	require.NoError(t, err)

	assert.Equal(t, 3, total, "total counts every match, not just the page")
	require.Len(t, firstPage, 2)
	require.Len(t, secondPage, 1)
	assert.Equal(t, "A-3", secondPage[0].PatientHN)
}
