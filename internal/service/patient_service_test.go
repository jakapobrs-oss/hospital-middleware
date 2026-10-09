package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
	"github.com/jakapobrs-oss/hospital-middleware/internal/service"
)

var (
	hospitalAStaff = domain.StaffIdentity{StaffID: 1, HospitalID: 10, HospitalCode: "hospital-a", Username: "nurse.a"}
	hospitalBStaff = domain.StaffIdentity{StaffID: 2, HospitalID: 20, HospitalCode: "hospital-b", Username: "nurse.b"}
)

// fakePatientRepository records calls and serves patients stored per hospital.
type fakePatientRepository struct {
	patientsByHospitalID map[int64][]domain.Patient
	searchErr            error
	upsertErr            error

	searchedHospitalID int64
	searchedCriteria   domain.PatientSearchCriteria
	searchHadDeadline  bool
	upserted           []domain.Patient
}

func (repository *fakePatientRepository) Search(ctx context.Context, hospitalID int64, criteria domain.PatientSearchCriteria) ([]domain.Patient, int, error) {
	repository.searchedHospitalID = hospitalID
	repository.searchedCriteria = criteria
	_, repository.searchHadDeadline = ctx.Deadline()
	if repository.searchErr != nil {
		return nil, 0, repository.searchErr
	}
	// Identity filters are applied like the real repository; other filters are not modelled.
	var patients []domain.Patient
	for _, patient := range repository.patientsByHospitalID[hospitalID] {
		if criteria.NationalID != "" && patient.NationalID != criteria.NationalID {
			continue
		}
		if criteria.PassportID != "" && patient.PassportID != criteria.PassportID {
			continue
		}
		patients = append(patients, patient)
	}
	return patients, len(patients), nil
}

func (repository *fakePatientRepository) UpsertFromHIS(_ context.Context, patient domain.Patient) error {
	if repository.upsertErr != nil {
		return repository.upsertErr
	}
	repository.upserted = append(repository.upserted, patient)
	return nil
}

// fakeHISClient answers from a map of identity number -> patient.
type fakeHISClient struct {
	patientsByIdentityNumber map[string]domain.Patient
	err                      error
	requestedIdentityNumbers []string
}

func (client *fakeHISClient) FindPatient(_ context.Context, identityNumber string) (domain.Patient, error) {
	client.requestedIdentityNumbers = append(client.requestedIdentityNumbers, identityNumber)
	if client.err != nil {
		return domain.Patient{}, client.err
	}
	patient, found := client.patientsByIdentityNumber[identityNumber]
	if !found {
		return domain.Patient{}, domain.ErrPatientNotFound
	}
	return patient, nil
}

type fakeHISRegistry map[string]his.Client

func (registry fakeHISRegistry) ClientFor(hospitalCode string) (his.Client, bool) {
	client, found := registry[hospitalCode]
	return client, found
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPatientService_Search_IsScopedToTheStaffHospital(t *testing.T) {
	repository := &fakePatientRepository{patientsByHospitalID: map[int64][]domain.Patient{
		10: {{PatientHN: "HN-A-1", HospitalID: 10}},
		20: {{PatientHN: "HN-B-1", HospitalID: 20}},
	}}
	patientService := service.NewPatientService(repository, fakeHISRegistry{}, discardLogger())

	result, err := patientService.Search(context.Background(), hospitalBStaff, domain.PatientSearchCriteria{FirstName: "som"})

	require.NoError(t, err)
	assert.Equal(t, int64(20), repository.searchedHospitalID)
	require.Len(t, result.Patients, 1)
	assert.Equal(t, "HN-B-1", result.Patients[0].PatientHN)
	assert.Equal(t, 1, result.Total)
}

func TestPatientService_Search_AppliesPaginationDefaults(t *testing.T) {
	testCases := []struct {
		name           string
		criteria       domain.PatientSearchCriteria
		expectedLimit  int
		expectedOffset int
	}{
		{name: "defaults when not set", criteria: domain.PatientSearchCriteria{}, expectedLimit: 20, expectedOffset: 0},
		{name: "caps the page size", criteria: domain.PatientSearchCriteria{Limit: 1000, Offset: 5}, expectedLimit: 100, expectedOffset: 5},
		{name: "negative offset becomes zero", criteria: domain.PatientSearchCriteria{Limit: 10, Offset: -3}, expectedLimit: 10, expectedOffset: 0},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakePatientRepository{}
			patientService := service.NewPatientService(repository, fakeHISRegistry{}, discardLogger())

			result, err := patientService.Search(context.Background(), hospitalAStaff, testCase.criteria)

			require.NoError(t, err)
			assert.Equal(t, testCase.expectedLimit, repository.searchedCriteria.Limit)
			assert.Equal(t, testCase.expectedOffset, repository.searchedCriteria.Offset)
			assert.Equal(t, testCase.expectedLimit, result.Limit)
			assert.Equal(t, testCase.expectedOffset, result.Offset)
		})
	}
}

func TestPatientService_Search_RefreshesFromHISWhenSearchingByNationalID(t *testing.T) {
	hisPatient := domain.Patient{PatientHN: "HN-A-4", NationalID: "1100000000032", FirstNameEN: "Wichai"}
	hisClient := &fakeHISClient{patientsByIdentityNumber: map[string]domain.Patient{"1100000000032": hisPatient}}
	repository := &fakePatientRepository{}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hisClient}, discardLogger())

	_, err := patientService.Search(context.Background(), hospitalAStaff, domain.PatientSearchCriteria{NationalID: "1100000000032"})

	require.NoError(t, err)
	require.Len(t, repository.upserted, 1)
	assert.Equal(t, "HN-A-4", repository.upserted[0].PatientHN)
	assert.Equal(t, int64(10), repository.upserted[0].HospitalID, "the record is stored under the staff member's hospital")
	assert.Equal(t, int64(10), repository.searchedHospitalID)
}

func TestPatientService_Search_ChecksEveryIdentityNumberWithHIS(t *testing.T) {
	somchai := domain.Patient{PatientHN: "HN-A-1", NationalID: "1100000000016", PassportID: "AA0000001"}
	hisClient := &fakeHISClient{patientsByIdentityNumber: map[string]domain.Patient{
		"1100000000016": somchai,
		"AA0000001":     somchai,
	}}
	repository := &fakePatientRepository{}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hisClient}, discardLogger())

	_, err := patientService.Search(context.Background(), hospitalAStaff,
		domain.PatientSearchCriteria{NationalID: "1100000000016", PassportID: "AA0000001"})

	require.NoError(t, err)
	assert.Equal(t, []string{"1100000000016", "AA0000001"}, hisClient.requestedIdentityNumbers)
	assert.Len(t, repository.upserted, 2)
	assert.True(t, repository.searchHadDeadline, "database calls must be bounded by a timeout")
}

func TestPatientService_Search_HISNotFoundHidesStaleLocalCopy(t *testing.T) {
	hisClient := &fakeHISClient{} // knows nobody
	repository := &fakePatientRepository{patientsByHospitalID: map[int64][]domain.Patient{
		10: {{PatientHN: "HN-A-OLD", NationalID: "1100000000016"}},
	}}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hisClient}, discardLogger())

	result, err := patientService.Search(context.Background(), hospitalAStaff, domain.PatientSearchCriteria{NationalID: "1100000000016"})

	require.NoError(t, err)
	assert.Empty(t, result.Patients, "the HIS is the source of truth; its 'not found' outranks the local copy")
	assert.NotNil(t, result.Patients, "an empty page is [] in JSON, not null")
	assert.Equal(t, 0, result.Total)
	assert.Equal(t, int64(0), repository.searchedHospitalID, "no database search is needed")
}

func TestPatientService_Search_DoesNotCallHISWithoutIdentityNumber(t *testing.T) {
	hisClient := &fakeHISClient{}
	repository := &fakePatientRepository{}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hisClient}, discardLogger())

	_, err := patientService.Search(context.Background(), hospitalAStaff, domain.PatientSearchCriteria{LastName: "jaidee"})

	require.NoError(t, err)
	assert.Empty(t, hisClient.requestedIdentityNumbers)
	assert.Empty(t, repository.upserted)
}

func TestPatientService_Search_UsesOnlyTheStaffHospitalHIS(t *testing.T) {
	hospitalAHIS := &fakeHISClient{patientsByIdentityNumber: map[string]domain.Patient{
		"1100000000016": {PatientHN: "HN-A-1", NationalID: "1100000000016"},
	}}
	repository := &fakePatientRepository{}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hospitalAHIS}, discardLogger())

	_, err := patientService.Search(context.Background(), hospitalBStaff, domain.PatientSearchCriteria{NationalID: "1100000000016"})

	require.NoError(t, err)
	assert.Empty(t, hospitalAHIS.requestedIdentityNumbers, "Hospital B staff must never trigger Hospital A's HIS")
	assert.Empty(t, repository.upserted)
	assert.Equal(t, int64(20), repository.searchedHospitalID)
}

func TestPatientService_Search_StillAnswersWhenHISFails(t *testing.T) {
	hisClient := &fakeHISClient{err: errors.New("his: request failed: timeout")}
	repository := &fakePatientRepository{patientsByHospitalID: map[int64][]domain.Patient{
		10: {{PatientHN: "HN-A-1", NationalID: "1100000000016"}},
	}}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hisClient}, discardLogger())

	result, err := patientService.Search(context.Background(), hospitalAStaff, domain.PatientSearchCriteria{NationalID: "1100000000016"})

	require.NoError(t, err)
	assert.Len(t, result.Patients, 1)
	assert.Empty(t, repository.upserted)
}

func TestPatientService_Search_FailsWhenHISRecordCannotBeStored(t *testing.T) {
	hisClient := &fakeHISClient{patientsByIdentityNumber: map[string]domain.Patient{
		"1100000000016": {PatientHN: "HN-A-1", NationalID: "1100000000016"},
	}}
	storageErr := errors.New("database is read-only")
	repository := &fakePatientRepository{upsertErr: storageErr}
	patientService := service.NewPatientService(repository, fakeHISRegistry{"hospital-a": hisClient}, discardLogger())

	_, err := patientService.Search(context.Background(), hospitalAStaff, domain.PatientSearchCriteria{NationalID: "1100000000016"})

	assert.ErrorIs(t, err, storageErr, "a patient the HIS found must not be reported as 'no match'")
}

func TestPatientService_FindByIdentityNumber(t *testing.T) {
	somchai := domain.Patient{PatientHN: "HN-A-1", NationalID: "1100000000016"}
	john := domain.Patient{PatientHN: "HN-A-3", PassportID: "AA1234567"}

	testCases := []struct {
		name             string
		identityNumber   string
		expectedCriteria domain.PatientSearchCriteria
		expectedPatient  domain.Patient
	}{
		{name: "national ID with dashes", identityNumber: "1-1000-00000-01-6",
			expectedCriteria: domain.PatientSearchCriteria{NationalID: "1100000000016", Limit: 1}, expectedPatient: somchai},
		{name: "passport, any case", identityNumber: "aa1234567",
			expectedCriteria: domain.PatientSearchCriteria{PassportID: "AA1234567", Limit: 1}, expectedPatient: john},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakePatientRepository{patientsByHospitalID: map[int64][]domain.Patient{10: {testCase.expectedPatient}}}
			patientService := service.NewPatientService(repository, fakeHISRegistry{}, discardLogger())

			patient, err := patientService.FindByIdentityNumber(context.Background(), hospitalAStaff, testCase.identityNumber)

			require.NoError(t, err)
			assert.Equal(t, testCase.expectedPatient, patient)
			assert.Equal(t, testCase.expectedCriteria, repository.searchedCriteria)
			assert.Equal(t, int64(10), repository.searchedHospitalID)
		})
	}
}

func TestPatientService_FindByIdentityNumber_ThirteenDigitPassport(t *testing.T) {
	numericPassportHolder := domain.Patient{PatientHN: "HN-A-9", PassportID: "1234567890123"}
	repository := &fakePatientRepository{patientsByHospitalID: map[int64][]domain.Patient{10: {numericPassportHolder}}}
	patientService := service.NewPatientService(repository, fakeHISRegistry{}, discardLogger())

	patient, err := patientService.FindByIdentityNumber(context.Background(), hospitalAStaff, "1234567890123")

	require.NoError(t, err, "a 13-digit number that is not a national ID is tried as a passport")
	assert.Equal(t, numericPassportHolder, patient)
}

func TestPatientService_FindByIdentityNumber_NotFound(t *testing.T) {
	patientService := service.NewPatientService(&fakePatientRepository{}, fakeHISRegistry{}, discardLogger())

	_, err := patientService.FindByIdentityNumber(context.Background(), hospitalAStaff, "1100000000016")

	assert.ErrorIs(t, err, domain.ErrPatientNotFound)
}

func TestPatientService_FindByIdentityNumber_PropagatesErrors(t *testing.T) {
	databaseErr := errors.New("connection refused")
	patientService := service.NewPatientService(&fakePatientRepository{searchErr: databaseErr}, fakeHISRegistry{}, discardLogger())

	_, err := patientService.FindByIdentityNumber(context.Background(), hospitalAStaff, "AA1234567")

	assert.ErrorIs(t, err, databaseErr)
}

func TestPatientService_Search_ReturnsRepositoryError(t *testing.T) {
	databaseErr := errors.New("connection refused")
	repository := &fakePatientRepository{searchErr: databaseErr}
	patientService := service.NewPatientService(repository, fakeHISRegistry{}, discardLogger())

	_, err := patientService.Search(context.Background(), hospitalAStaff, domain.PatientSearchCriteria{})

	assert.ErrorIs(t, err, databaseErr)
}
