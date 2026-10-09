package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
)

// databaseTimeout bounds the database work of one request, so slow queries or locks cannot pile up
// requests and exhaust the connection pool.
const databaseTimeout = 5 * time.Second

// PatientRepository is the storage the patient service needs.
type PatientRepository interface {
	Search(ctx context.Context, hospitalID int64, criteria domain.PatientSearchCriteria) ([]domain.Patient, int, error)
	UpsertFromHIS(ctx context.Context, patient domain.Patient) error
}

// HISClientRegistry finds the HIS client of a hospital.
type HISClientRegistry interface {
	ClientFor(hospitalCode string) (his.Client, bool)
}

// hisLookupOutcome is what the HIS said about the identity numbers of a search.
type hisLookupOutcome int

const (
	// hisNotConsulted: no HIS is configured for the hospital, or it was unavailable — search local data.
	hisNotConsulted hisLookupOutcome = iota
	// hisRecordStored: the HIS returned the patient and the local copy is now up to date.
	hisRecordStored
	// hisConfirmedNotFound: the HIS answered that no patient has the requested identity number.
	hisConfirmedNotFound
)

// PatientService answers patient searches for authenticated staff.
type PatientService struct {
	patients   PatientRepository
	hisClients HISClientRegistry
	logger     *slog.Logger
}

// NewPatientService creates a PatientService.
func NewPatientService(patients PatientRepository, hisClients HISClientRegistry, logger *slog.Logger) *PatientService {
	return &PatientService{patients: patients, hisClients: hisClients, logger: logger}
}

// Search returns the patients of the staff member's own hospital that match criteria.
//
// When the criteria contain a national ID or passport ID, the hospital's HIS is asked first, because it
// is the source of truth and can only be queried by those numbers:
//   - a record it returns is stored locally before the search, so the answer is current;
//   - "not found" means any local copy with that number is stale, so the answer is empty;
//   - an unavailable HIS does not block staff: the search answers from the local copy.
func (service *PatientService) Search(ctx context.Context, staff domain.StaffIdentity, criteria domain.PatientSearchCriteria) (domain.PatientSearchResult, error) {
	criteria = withPaginationDefaults(criteria)
	emptyResult := domain.PatientSearchResult{Patients: []domain.Patient{}, Limit: criteria.Limit, Offset: criteria.Offset}

	if criteria.HasIdentityNumber() {
		outcome, err := service.refreshFromHIS(ctx, staff, criteria)
		if err != nil {
			return domain.PatientSearchResult{}, err
		}
		if outcome == hisConfirmedNotFound {
			return emptyResult, nil
		}
	}

	databaseCtx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	// The hospital always comes from the authenticated staff member, never from the request.
	patients, total, err := service.patients.Search(databaseCtx, staff.HospitalID, criteria)
	if err != nil {
		return domain.PatientSearchResult{}, fmt.Errorf("patient service: search: %w", err)
	}
	return domain.PatientSearchResult{
		Patients: patients,
		Total:    total,
		Limit:    criteria.Limit,
		Offset:   criteria.Offset,
	}, nil
}

// FindByIdentityNumber returns the patient of the staff member's hospital whose national ID or passport ID
// is identityNumber, refreshed from the HIS first. It mirrors the HIS route GET /patient/search/{id}, which
// accepts either kind of number. It returns domain.ErrPatientNotFound when there is no such patient.
func (service *PatientService) FindByIdentityNumber(ctx context.Context, staff domain.StaffIdentity, identityNumber string) (domain.Patient, error) {
	if domain.LooksLikeNationalID(identityNumber) {
		patient, err := service.findFirst(ctx, staff, domain.PatientSearchCriteria{NationalID: domain.NormalizeNationalID(identityNumber)})
		if !errors.Is(err, domain.ErrPatientNotFound) {
			return patient, err
		}
		// Some passports are 13 digits too, so a miss as a national ID is tried again as a passport number.
	}
	return service.findFirst(ctx, staff, domain.PatientSearchCriteria{PassportID: domain.NormalizePassportID(identityNumber)})
}

// findFirst runs a one-row search and turns "no rows" into domain.ErrPatientNotFound.
func (service *PatientService) findFirst(ctx context.Context, staff domain.StaffIdentity, criteria domain.PatientSearchCriteria) (domain.Patient, error) {
	criteria.Limit = 1
	result, err := service.Search(ctx, staff, criteria)
	if err != nil {
		return domain.Patient{}, err
	}
	if len(result.Patients) == 0 {
		return domain.Patient{}, domain.ErrPatientNotFound
	}
	return result.Patients[0], nil
}

// refreshFromHIS asks the staff hospital's HIS about the identity numbers in criteria and stores the
// record it returns. Only a failure to store that record is an error; an unavailable HIS is not.
func (service *PatientService) refreshFromHIS(ctx context.Context, staff domain.StaffIdentity, criteria domain.PatientSearchCriteria) (hisLookupOutcome, error) {
	hisClient, found := service.hisClients.ClientFor(staff.HospitalCode)
	if !found {
		return hisNotConsulted, nil // this hospital has no HIS integration configured
	}

	for _, identityNumber := range identityNumbersOf(criteria) {
		patient, err := hisClient.FindPatient(ctx, identityNumber)
		if errors.Is(err, domain.ErrPatientNotFound) {
			// Every filter must match, so a patient unknown to the HIS cannot be in the answer.
			return hisConfirmedNotFound, nil
		}
		if err != nil {
			service.logger.WarnContext(ctx, "HIS lookup failed, answering from local data",
				"hospital_code", staff.HospitalCode, "error", err)
			return hisNotConsulted, nil
		}

		patient.HospitalID = staff.HospitalID
		databaseCtx, cancel := context.WithTimeout(ctx, databaseTimeout)
		err = service.patients.UpsertFromHIS(databaseCtx, patient)
		cancel()
		if err != nil {
			return hisNotConsulted, fmt.Errorf("patient service: store HIS record: %w", err)
		}
	}
	return hisRecordStored, nil
}

// identityNumbersOf lists the identity numbers to check with the HIS, national ID first.
func identityNumbersOf(criteria domain.PatientSearchCriteria) []string {
	var identityNumbers []string
	if criteria.NationalID != "" {
		identityNumbers = append(identityNumbers, criteria.NationalID)
	}
	if criteria.PassportID != "" {
		identityNumbers = append(identityNumbers, criteria.PassportID)
	}
	return identityNumbers
}

func withPaginationDefaults(criteria domain.PatientSearchCriteria) domain.PatientSearchCriteria {
	if criteria.Limit <= 0 {
		criteria.Limit = domain.DefaultSearchLimit
	}
	if criteria.Limit > domain.MaxSearchLimit {
		criteria.Limit = domain.MaxSearchLimit
	}
	if criteria.Offset < 0 {
		criteria.Offset = 0
	}
	return criteria
}
