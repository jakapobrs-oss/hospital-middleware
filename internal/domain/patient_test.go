package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

func TestNormalizeNationalID(t *testing.T) {
	assert.Equal(t, "1100000000016", domain.NormalizeNationalID("1-1000-00000-01-6"))
	assert.Equal(t, "1100000000016", domain.NormalizeNationalID(" 1100000000016 "))
	assert.Equal(t, "", domain.NormalizeNationalID("no digits"))
}

func TestNormalizePassportID(t *testing.T) {
	assert.Equal(t, "AA1234567", domain.NormalizePassportID(" aa1234567 "))
}

func TestLooksLikeNationalID(t *testing.T) {
	testCases := map[string]bool{
		"1100000000016":     true,
		"1-1000-00000-01-6": true,
		"1 1000 00000 01 6": true,
		"AA1234567":         false,
		"110000000001":      false, // 12 digits
		"11000000000166":    false, // 14 digits
		"1100000000O16":     false, // letter O
	}
	for identifier, expected := range testCases {
		assert.Equal(t, expected, domain.LooksLikeNationalID(identifier), identifier)
	}
}

func TestPatientSearchCriteria_HasIdentityNumber(t *testing.T) {
	assert.False(t, domain.PatientSearchCriteria{FirstName: "som"}.HasIdentityNumber())
	assert.True(t, domain.PatientSearchCriteria{NationalID: "1100000000016"}.HasIdentityNumber())
	assert.True(t, domain.PatientSearchCriteria{PassportID: "AA1234567"}.HasIdentityNumber())
}
