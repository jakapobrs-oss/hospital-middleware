package domain

import (
	"strings"
	"time"
)

// Gender follows the HIS contract: "M" or "F". An empty value means unknown.
type Gender string

const (
	GenderMale   Gender = "M"
	GenderFemale Gender = "F"
)

// Patient mirrors the fields returned by a Hospital Information System (HIS),
// plus the hospital that owns the record. Optional text fields use "" for "not provided".
type Patient struct {
	ID           int64
	HospitalID   int64
	PatientHN    string // hospital number, unique within a hospital
	NationalID   string
	PassportID   string
	FirstNameTH  string
	MiddleNameTH string
	LastNameTH   string
	FirstNameEN  string
	MiddleNameEN string
	LastNameEN   string
	DateOfBirth  *time.Time // nil when the HIS does not provide it
	PhoneNumber  string
	Email        string
	Gender       Gender
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Pagination bounds for patient search.
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 100
)

// PatientSearchCriteria holds the optional filters of /patient/search.
// Empty strings and a nil DateOfBirth mean "no filter on this field".
type PatientSearchCriteria struct {
	NationalID  string
	PassportID  string
	FirstName   string // matched against both Thai and English first names
	MiddleName  string // matched against both Thai and English middle names
	LastName    string // matched against both Thai and English last names
	DateOfBirth *time.Time
	PhoneNumber string
	Email       string
	Limit       int
	Offset      int
}

// HasIdentityNumber reports whether the search can be answered by an HIS lookup.
func (criteria PatientSearchCriteria) HasIdentityNumber() bool {
	return criteria.NationalID != "" || criteria.PassportID != ""
}

// PatientSearchResult is one page of matching patients.
type PatientSearchResult struct {
	Patients []Patient
	Total    int // total matches across all pages
	Limit    int
	Offset   int
}

// NationalIDLength is the number of digits in a Thai national ID.
const NationalIDLength = 13

// LooksLikeNationalID reports whether identifier is a Thai national ID: 13 digits, optionally
// separated by dashes or spaces. Anything else (for example "AA1234567") is treated as a passport number.
func LooksLikeNationalID(identifier string) bool {
	for _, character := range identifier {
		isDigit := character >= '0' && character <= '9'
		if !isDigit && character != '-' && character != ' ' {
			return false
		}
	}
	return len(NormalizeNationalID(identifier)) == NationalIDLength
}

// NormalizeNationalID keeps digits only, so "1-1000-00000-01-6" and "1100000000016" are the same ID.
func NormalizeNationalID(nationalID string) string {
	var digits strings.Builder
	for _, character := range nationalID {
		if character >= '0' && character <= '9' {
			digits.WriteRune(character)
		}
	}
	return digits.String()
}

// NormalizePassportID trims spaces and upper-cases the passport number.
func NormalizePassportID(passportID string) string {
	return strings.ToUpper(strings.TrimSpace(passportID))
}
