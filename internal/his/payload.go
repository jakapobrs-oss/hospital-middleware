package his

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// DateLayout is the date format used by the HIS for date_of_birth.
const DateLayout = "2006-01-02"

// buddhistEraOffset converts a Thai Buddhist Era year to the Gregorian year (2533 BE = 1990 CE).
const buddhistEraOffset = 543

// firstBuddhistEraYear: no living patient was born in Gregorian year 2400 or later, so such a year must be BE.
const firstBuddhistEraYear = 2400

// acceptedDateLayouts are the date_of_birth formats tolerated from an HIS, most specific first.
var acceptedDateLayouts = []string{DateLayout, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"}

// datePrefixPattern captures the calendar date every accepted layout starts with. The date is read as
// written (no time-zone conversion): a birthday does not move with the zone of a timestamp.
var datePrefixPattern = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)

const leapYearForShapeCheck = "2000"

// PatientPayload is the JSON body of GET /patient/search/{id} in the Hospital A HIS API.
// Absent values may arrive as null or as an empty string; both decode to "".
type PatientPayload struct {
	FirstNameTH  string `json:"first_name_th"`
	MiddleNameTH string `json:"middle_name_th"`
	LastNameTH   string `json:"last_name_th"`
	FirstNameEN  string `json:"first_name_en"`
	MiddleNameEN string `json:"middle_name_en"`
	LastNameEN   string `json:"last_name_en"`
	DateOfBirth  string `json:"date_of_birth"`
	PatientHN    string `json:"patient_hn"`
	NationalID   string `json:"national_id"`
	PassportID   string `json:"passport_id"`
	PhoneNumber  string `json:"phone_number"`
	Email        string `json:"email"`
	Gender       string `json:"gender"`
}

// ToDomain validates the payload and converts it into a domain patient (without HospitalID).
func (payload PatientPayload) ToDomain() (domain.Patient, error) {
	patient := domain.Patient{
		PatientHN:    strings.TrimSpace(payload.PatientHN),
		NationalID:   domain.NormalizeNationalID(payload.NationalID),
		PassportID:   domain.NormalizePassportID(payload.PassportID),
		FirstNameTH:  strings.TrimSpace(payload.FirstNameTH),
		MiddleNameTH: strings.TrimSpace(payload.MiddleNameTH),
		LastNameTH:   strings.TrimSpace(payload.LastNameTH),
		FirstNameEN:  strings.TrimSpace(payload.FirstNameEN),
		MiddleNameEN: strings.TrimSpace(payload.MiddleNameEN),
		LastNameEN:   strings.TrimSpace(payload.LastNameEN),
		PhoneNumber:  strings.TrimSpace(payload.PhoneNumber),
		Email:        strings.TrimSpace(payload.Email),
		Gender:       normalizeGender(payload.Gender),
	}

	if patient.PatientHN == "" {
		return domain.Patient{}, errors.New("his payload: patient_hn is missing")
	}
	if patient.NationalID == "" && patient.PassportID == "" {
		return domain.Patient{}, errors.New("his payload: both national_id and passport_id are missing")
	}

	// An unreadable date of birth must not cost the whole record: it is stored as unknown instead.
	patient.DateOfBirth = parseDateOfBirth(payload.DateOfBirth)
	return patient, nil
}

// parseDateOfBirth reads the HIS date in any accepted layout, converts Buddhist Era years to
// Gregorian, and returns nil when the value is empty, unreadable or not a real calendar day.
//
// The calendar check must happen after the era conversion: 2543-02-29 BE is 29 Feb 2000 (a leap
// day), while 2544-02-29 BE would be 29 Feb 2001, which does not exist.
func parseDateOfBirth(rawDate string) *time.Time {
	rawDate = strings.TrimSpace(rawDate)
	dateParts := datePrefixPattern.FindStringSubmatch(rawDate)
	if dateParts == nil || !hasAcceptedLayout(rawDate) {
		return nil
	}

	year, _ := strconv.Atoi(dateParts[1])
	month, _ := strconv.Atoi(dateParts[2])
	day, _ := strconv.Atoi(dateParts[3])
	if year >= firstBuddhistEraYear {
		year -= buddhistEraOffset
	}

	dateOfBirth := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	// time.Date silently rolls invalid days over (31 April → 1 May); such a date is rejected instead.
	if dateOfBirth.Year() != year || int(dateOfBirth.Month()) != month || dateOfBirth.Day() != day {
		return nil
	}
	return &dateOfBirth
}

// hasAcceptedLayout checks the shape of rawDate against the accepted layouts. The year is swapped for
// 2000 (a leap year) so that era conversion and the calendar check stay with parseDateOfBirth.
func hasAcceptedLayout(rawDate string) bool {
	shapeOnly := leapYearForShapeCheck + rawDate[len(leapYearForShapeCheck):]
	for _, layout := range acceptedDateLayouts {
		if _, err := time.Parse(layout, shapeOnly); err == nil {
			return true
		}
	}
	return false
}

// PayloadFromDomain converts a domain patient into the HIS wire format (used by the mock HIS).
func PayloadFromDomain(patient domain.Patient) PatientPayload {
	payload := PatientPayload{
		FirstNameTH:  patient.FirstNameTH,
		MiddleNameTH: patient.MiddleNameTH,
		LastNameTH:   patient.LastNameTH,
		FirstNameEN:  patient.FirstNameEN,
		MiddleNameEN: patient.MiddleNameEN,
		LastNameEN:   patient.LastNameEN,
		PatientHN:    patient.PatientHN,
		NationalID:   patient.NationalID,
		PassportID:   patient.PassportID,
		PhoneNumber:  patient.PhoneNumber,
		Email:        patient.Email,
		Gender:       string(patient.Gender),
	}
	if patient.DateOfBirth != nil {
		payload.DateOfBirth = patient.DateOfBirth.Format(DateLayout)
	}
	return payload
}

// normalizeGender keeps only the values the HIS contract allows ("M"/"F"); anything else is unknown.
func normalizeGender(rawGender string) domain.Gender {
	switch gender := domain.Gender(strings.ToUpper(strings.TrimSpace(rawGender))); gender {
	case domain.GenderMale, domain.GenderFemale:
		return gender
	default:
		return ""
	}
}
