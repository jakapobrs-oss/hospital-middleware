// Package demodata provides synthetic patients for demos and the mock HIS.
// Every record is fictional: example.com e-mails, 080-000-xxxx phones and patterned ID numbers.
package demodata

import (
	"context"
	"fmt"
	"time"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const (
	hospitalACode = "hospital-a"
	hospitalBCode = "hospital-b"
)

// HospitalFinder resolves a hospital code to the stored hospital.
type HospitalFinder interface {
	GetByCode(ctx context.Context, code string) (domain.Hospital, error)
}

// PatientUpserter stores a patient record, replacing the one with the same hospital and HN.
type PatientUpserter interface {
	UpsertFromHIS(ctx context.Context, patient domain.Patient) error
}

// Seed stores the demo patients of both hospitals. It is idempotent, so it is safe on every start-up.
func Seed(ctx context.Context, hospitals HospitalFinder, patients PatientUpserter) error {
	for hospitalCode, hospitalPatients := range seededPatientsByHospitalCode() {
		hospital, err := hospitals.GetByCode(ctx, hospitalCode)
		if err != nil {
			return fmt.Errorf("demo seed: find hospital %q: %w", hospitalCode, err)
		}
		for _, patient := range hospitalPatients {
			patient.HospitalID = hospital.ID
			if err := patients.UpsertFromHIS(ctx, patient); err != nil {
				return fmt.Errorf("demo seed: store patient %s: %w", patient.PatientHN, err)
			}
		}
	}
	return nil
}

// HospitalAHISPatients is what the mock Hospital A HIS knows: the seeded Hospital A patients plus
// one patient that reaches the middleware only after a search by national ID.
func HospitalAHISPatients() []domain.Patient {
	return append(hospitalAPatients(), domain.Patient{
		PatientHN:   "HN-A-000004",
		NationalID:  "1100000000032",
		FirstNameTH: "วิชัย",
		LastNameTH:  "มั่นคง",
		FirstNameEN: "Wichai",
		LastNameEN:  "Mankong",
		DateOfBirth: date(2000, time.July, 21),
		PhoneNumber: "080-000-0004",
		Email:       "wichai.m@example.com",
		Gender:      domain.GenderMale,
	})
}

func seededPatientsByHospitalCode() map[string][]domain.Patient {
	return map[string][]domain.Patient{
		hospitalACode: hospitalAPatients(),
		hospitalBCode: hospitalBPatients(),
	}
}

func hospitalAPatients() []domain.Patient {
	return []domain.Patient{
		{
			PatientHN:   "HN-A-000001",
			NationalID:  "1100000000016",
			FirstNameTH: "สมชาย",
			LastNameTH:  "ใจดี",
			FirstNameEN: "Somchai",
			LastNameEN:  "Jaidee",
			DateOfBirth: date(1985, time.April, 12),
			PhoneNumber: "080-000-0001",
			Email:       "somchai.j@example.com",
			Gender:      domain.GenderMale,
		},
		{
			PatientHN:    "HN-A-000002",
			NationalID:   "1100000000024",
			FirstNameTH:  "สมหญิง",
			MiddleNameTH: "มณี",
			LastNameTH:   "รักไทย",
			FirstNameEN:  "Somying",
			MiddleNameEN: "Manee",
			LastNameEN:   "Rakthai",
			DateOfBirth:  date(1990, time.November, 3),
			PhoneNumber:  "080-000-0002",
			Email:        "somying.r@example.com",
			Gender:       domain.GenderFemale,
		},
		{
			// A foreign patient: passport only, no Thai national ID.
			PatientHN:    "HN-A-000003",
			PassportID:   "AA1234567",
			FirstNameTH:  "จอห์น",
			MiddleNameTH: "ไมเคิล",
			LastNameTH:   "สมิธ",
			FirstNameEN:  "John",
			MiddleNameEN: "Michael",
			LastNameEN:   "Smith",
			DateOfBirth:  date(1978, time.February, 28),
			PhoneNumber:  "080-000-0003",
			Email:        "john.smith@example.com",
			Gender:       domain.GenderMale,
		},
	}
}

func hospitalBPatients() []domain.Patient {
	return []domain.Patient{
		{
			// The same person as HN-A-000001, registered separately at Hospital B with its own HN.
			PatientHN:   "HN-B-000001",
			NationalID:  "1100000000016",
			FirstNameTH: "สมชาย",
			LastNameTH:  "ใจดี",
			FirstNameEN: "Somchai",
			LastNameEN:  "Jaidee",
			DateOfBirth: date(1985, time.April, 12),
			PhoneNumber: "080-000-0001",
			Email:       "somchai.j@example.com",
			Gender:      domain.GenderMale,
		},
		{
			PatientHN:   "HN-B-000002",
			NationalID:  "1100000000041",
			FirstNameTH: "มาลี",
			LastNameTH:  "ศรีสุข",
			FirstNameEN: "Malee",
			LastNameEN:  "Srisuk",
			DateOfBirth: date(1995, time.January, 15),
			PhoneNumber: "080-000-0005",
			Email:       "malee.s@example.com",
			Gender:      domain.GenderFemale,
		},
	}
}

func date(year int, month time.Month, day int) *time.Time {
	value := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &value
}
