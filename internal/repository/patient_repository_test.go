package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

func TestBuildPatientFilter_AlwaysScopesToHospital(t *testing.T) {
	filter := buildPatientFilter(42, domain.PatientSearchCriteria{})

	assert.Equal(t, "hospital_id = $1", filter.whereClause)
	assert.Equal(t, []any{int64(42)}, filter.args)
}

func TestBuildPatientFilter_CombinesEveryCriterion(t *testing.T) {
	dateOfBirth := time.Date(1985, time.April, 12, 0, 0, 0, 0, time.UTC)
	criteria := domain.PatientSearchCriteria{
		NationalID:  "1100000000016",
		PassportID:  "AA1234567",
		FirstName:   "som",
		MiddleName:  "mid",
		LastName:    "jai",
		DateOfBirth: &dateOfBirth,
		PhoneNumber: "080-000-0001",
		Email:       "Somchai.J@example.com",
	}

	filter := buildPatientFilter(10, criteria)

	expectedWhere := "hospital_id = $1" +
		" AND national_id = $2" +
		" AND passport_id = $3" +
		" AND (first_name_th ILIKE $4 OR first_name_en ILIKE $4)" +
		" AND (middle_name_th ILIKE $5 OR middle_name_en ILIKE $5)" +
		" AND (last_name_th ILIKE $6 OR last_name_en ILIKE $6)" +
		" AND date_of_birth = $7" +
		" AND regexp_replace(phone_number, '[^0-9]', '', 'g') = ANY($8)" +
		" AND lower(email) = lower($9::text)"
	assert.Equal(t, expectedWhere, filter.whereClause)
	assert.Equal(t, []any{
		int64(10), "1100000000016", "AA1234567", "%som%", "%mid%", "%jai%",
		dateOfBirth, []string{"0800000001", "66800000001"}, "Somchai.J@example.com",
	}, filter.args)
}

func TestBuildPatientFilter_UppercasesPassport(t *testing.T) {
	filter := buildPatientFilter(10, domain.PatientSearchCriteria{PassportID: " aa1234567 "})

	assert.Equal(t, []any{int64(10), "AA1234567"}, filter.args)
}

func TestPhoneNumberVariants(t *testing.T) {
	testCases := map[string][]string{
		"080-000-0001":    {"0800000001", "66800000001"},
		"+66 80 000 0001": {"66800000001", "0800000001"},
		"021234567":       {"021234567", "6621234567"},
		"1669":            {"1669"},
	}
	for phoneNumber, expected := range testCases {
		assert.Equal(t, expected, phoneNumberVariants(phoneNumber), phoneNumber)
	}
}

func TestContainsPattern_EscapesLikeWildcards(t *testing.T) {
	testCases := map[string]string{
		"somchai":  "%somchai%",
		"50%":      `%50\%%`,
		"a_b":      `%a\_b%`,
		`back\sla`: `%back\\sla%`,
	}
	for input, expected := range testCases {
		assert.Equal(t, expected, containsPattern(input), "input %q", input)
	}
}

func TestToMigrateURL(t *testing.T) {
	assert.Equal(t, "pgx5://user:pass@db:5432/app", toMigrateURL("postgres://user:pass@db:5432/app"))
	assert.Equal(t, "pgx5://user:pass@db:5432/app", toMigrateURL("postgresql://user:pass@db:5432/app"))
	assert.Equal(t, "pgx5://already", toMigrateURL("pgx5://already"))
}
