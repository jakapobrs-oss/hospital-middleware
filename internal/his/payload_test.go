package his_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
)

func TestPatientPayload_ToDomain_NormalisesValues(t *testing.T) {
	payload := his.PatientPayload{
		PatientHN:   " HN-A-000009 ",
		NationalID:  "1-1000-00000-01-6",
		PassportID:  " ab123 ",
		FirstNameEN: " Somchai ",
		Gender:      "f",
	}

	patient, err := payload.ToDomain()

	require.NoError(t, err)
	assert.Equal(t, "HN-A-000009", patient.PatientHN)
	assert.Equal(t, "1100000000016", patient.NationalID)
	assert.Equal(t, "AB123", patient.PassportID)
	assert.Equal(t, "Somchai", patient.FirstNameEN)
	assert.Equal(t, domain.GenderFemale, patient.Gender)
	assert.Nil(t, patient.DateOfBirth)
}

func TestPatientPayload_ToDomain_DateOfBirthFormats(t *testing.T) {
	testCases := []struct {
		name     string
		rawDate  string
		expected string // "" means the date is stored as unknown
	}{
		{name: "plain date", rawDate: "1990-11-03", expected: "1990-11-03"},
		{name: "RFC 3339 timestamp", rawDate: "1990-11-03T00:00:00Z", expected: "1990-11-03"},
		{name: "timestamp without zone", rawDate: "1990-11-03 08:30:00", expected: "1990-11-03"},
		{name: "Thai Buddhist Era year", rawDate: "2533-11-03", expected: "1990-11-03"},
		{name: "Buddhist Era leap day (29 Feb 2000)", rawDate: "2543-02-29", expected: "2000-02-29"},
		{name: "Buddhist Era date that does not exist (29 Feb 2001)", rawDate: "2544-02-29", expected: ""},
		{name: "Gregorian leap day", rawDate: "2000-02-29", expected: "2000-02-29"},
		{name: "day that does not exist", rawDate: "1990-04-31", expected: ""},
		{name: "month that does not exist", rawDate: "1990-13-01", expected: ""},
		{name: "date as written, not shifted by the time zone", rawDate: "1990-11-03T23:30:00-05:00", expected: "1990-11-03"},
		{name: "unreadable date keeps the record", rawDate: "03/11/1990", expected: ""},
		{name: "empty", rawDate: "", expected: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := his.PatientPayload{PatientHN: "HN-1", NationalID: "1100000000016", DateOfBirth: testCase.rawDate}

			patient, err := payload.ToDomain()

			require.NoError(t, err)
			if testCase.expected == "" {
				assert.Nil(t, patient.DateOfBirth)
				return
			}
			require.NotNil(t, patient.DateOfBirth)
			assert.Equal(t, testCase.expected, patient.DateOfBirth.Format(his.DateLayout))
		})
	}
}

func TestPatientPayload_ToDomain_UnknownGenderBecomesEmpty(t *testing.T) {
	payload := his.PatientPayload{PatientHN: "HN-1", NationalID: "1100000000016", Gender: "X"}

	patient, err := payload.ToDomain()

	require.NoError(t, err)
	assert.Empty(t, patient.Gender)
}

func TestPatientPayload_ToDomain_RequiresAnIdentityNumber(t *testing.T) {
	_, err := his.PatientPayload{PatientHN: "HN-1"}.ToDomain()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "national_id and passport_id are missing")
}

func TestPayloadFromDomain_RoundTrips(t *testing.T) {
	dateOfBirth := time.Date(1990, time.November, 3, 0, 0, 0, 0, time.UTC)
	original := domain.Patient{
		PatientHN:    "HN-A-000002",
		NationalID:   "1100000000024",
		FirstNameTH:  "สมหญิง",
		MiddleNameTH: "มณี",
		LastNameTH:   "รักไทย",
		FirstNameEN:  "Somying",
		MiddleNameEN: "Manee",
		LastNameEN:   "Rakthai",
		DateOfBirth:  &dateOfBirth,
		PhoneNumber:  "080-000-0002",
		Email:        "somying.r@example.com",
		Gender:       domain.GenderFemale,
	}

	payload := his.PayloadFromDomain(original)
	assert.Equal(t, "1990-11-03", payload.DateOfBirth)

	roundTripped, err := payload.ToDomain()
	require.NoError(t, err)
	assert.Equal(t, original, roundTripped)
}
