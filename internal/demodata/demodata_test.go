package demodata_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/demodata"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

type fakeHospitalFinder map[string]domain.Hospital

func (hospitals fakeHospitalFinder) GetByCode(_ context.Context, code string) (domain.Hospital, error) {
	hospital, found := hospitals[code]
	if !found {
		return domain.Hospital{}, domain.ErrHospitalNotFound
	}
	return hospital, nil
}

type recordingUpserter struct {
	stored []domain.Patient
	err    error
}

func (upserter *recordingUpserter) UpsertFromHIS(_ context.Context, patient domain.Patient) error {
	if upserter.err != nil {
		return upserter.err
	}
	upserter.stored = append(upserter.stored, patient)
	return nil
}

var bothHospitals = fakeHospitalFinder{
	"hospital-a": {ID: 1, Code: "hospital-a"},
	"hospital-b": {ID: 2, Code: "hospital-b"},
}

func TestSeed_StoresPatientsUnderTheirHospital(t *testing.T) {
	upserter := &recordingUpserter{}

	require.NoError(t, demodata.Seed(context.Background(), bothHospitals, upserter))

	hospitalIDByHN := map[string]int64{}
	for _, patient := range upserter.stored {
		hospitalIDByHN[patient.PatientHN] = patient.HospitalID
	}
	assert.Equal(t, map[string]int64{
		"HN-A-000001": 1, "HN-A-000002": 1, "HN-A-000003": 1,
		"HN-B-000001": 2, "HN-B-000002": 2,
	}, hospitalIDByHN)
}

func TestSeed_FailsWhenHospitalIsMissing(t *testing.T) {
	err := demodata.Seed(context.Background(), fakeHospitalFinder{"hospital-a": {ID: 1}}, &recordingUpserter{})

	assert.ErrorIs(t, err, domain.ErrHospitalNotFound)
}

func TestSeed_FailsWhenStoringFails(t *testing.T) {
	storageErr := errors.New("database is read-only")

	err := demodata.Seed(context.Background(), bothHospitals, &recordingUpserter{err: storageErr})

	assert.ErrorIs(t, err, storageErr)
}

func TestHospitalAHISPatients_IncludesAPatientThatIsNotSeeded(t *testing.T) {
	upserter := &recordingUpserter{}
	require.NoError(t, demodata.Seed(context.Background(), bothHospitals, upserter))
	seededHNs := map[string]bool{}
	for _, patient := range upserter.stored {
		seededHNs[patient.PatientHN] = true
	}

	var hisOnly []string
	for _, patient := range demodata.HospitalAHISPatients() {
		if !seededHNs[patient.PatientHN] {
			hisOnly = append(hisOnly, patient.PatientHN)
		}
	}
	assert.Equal(t, []string{"HN-A-000004"}, hisOnly)
}

// The demo national IDs are synthetic but still pass the Thai national ID checksum, like real data would.
func TestDemoNationalIDsHaveValidChecksums(t *testing.T) {
	upserter := &recordingUpserter{}
	require.NoError(t, demodata.Seed(context.Background(), bothHospitals, upserter))

	for _, patient := range append(upserter.stored, demodata.HospitalAHISPatients()...) {
		if patient.NationalID == "" {
			continue
		}
		assert.True(t, hasValidThaiChecksum(patient.NationalID), "%s: %s", patient.PatientHN, patient.NationalID)
	}
}

func hasValidThaiChecksum(nationalID string) bool {
	if len(nationalID) != 13 {
		return false
	}
	weightedSum := 0
	for position := 0; position < 12; position++ {
		weightedSum += int(nationalID[position]-'0') * (13 - position)
	}
	checkDigit := (11 - weightedSum%11) % 10
	return checkDigit == int(nationalID[12]-'0')
}
