package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const patientColumns = `id, hospital_id, patient_hn, national_id, passport_id,
	first_name_th, middle_name_th, last_name_th, first_name_en, middle_name_en, last_name_en,
	date_of_birth, phone_number, email, gender, created_at, updated_at`

// PatientRepository stores the middleware's copy of HIS patient records.
type PatientRepository struct {
	pool *pgxpool.Pool
}

// NewPatientRepository creates a PatientRepository.
func NewPatientRepository(pool *pgxpool.Pool) *PatientRepository {
	return &PatientRepository{pool: pool}
}

// Search returns one page of a hospital's patients that match every non-empty criterion,
// plus the total number of matches across all pages. Both queries run in one read-only
// REPEATABLE READ transaction, so the total always describes the same data as the page.
func (repository *PatientRepository) Search(ctx context.Context, hospitalID int64, criteria domain.PatientSearchCriteria) ([]domain.Patient, int, error) {
	filter := buildPatientFilter(hospitalID, criteria)

	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("patient repository: begin search: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit

	var total int
	countQuery := "SELECT count(*) FROM patients WHERE " + filter.whereClause
	if err := tx.QueryRow(ctx, countQuery, filter.args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("patient repository: count: %w", err)
	}
	if total == 0 {
		return []domain.Patient{}, 0, nil
	}

	pageArgs := append(append([]any{}, filter.args...), criteria.Limit, criteria.Offset)
	pageQuery := fmt.Sprintf("SELECT %s FROM patients WHERE %s ORDER BY id LIMIT $%d OFFSET $%d",
		patientColumns, filter.whereClause, len(pageArgs)-1, len(pageArgs))
	rows, err := tx.Query(ctx, pageQuery, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("patient repository: search: %w", err)
	}
	patients, err := pgx.CollectRows(rows, scanPatient)
	if err != nil {
		return nil, 0, fmt.Errorf("patient repository: read rows: %w", err)
	}
	return patients, total, nil
}

// UpsertFromHIS inserts a patient received from the HIS, or refreshes the existing record
// with the same hospital and HN.
//
// The HIS is the source of truth: when it reports a national ID or passport under a different HN
// than the local copy (for example after the hospital merged two registrations), the old local row
// is stale. It is removed in the same transaction, otherwise the per-hospital unique indexes on the
// identity numbers would reject the fresh record and leave outdated data in place.
func (repository *PatientRepository) UpsertFromHIS(ctx context.Context, patient domain.Patient) error {
	const deleteStaleIdentityRowsQuery = `
		DELETE FROM patients
		WHERE hospital_id = $1 AND patient_hn <> $2 AND (national_id = $3 OR passport_id = $4)`
	const upsertQuery = `
		INSERT INTO patients (hospital_id, patient_hn, national_id, passport_id,
			first_name_th, middle_name_th, last_name_th, first_name_en, middle_name_en, last_name_en,
			date_of_birth, phone_number, email, gender)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (hospital_id, patient_hn) DO UPDATE SET
			national_id    = EXCLUDED.national_id,
			passport_id    = EXCLUDED.passport_id,
			first_name_th  = EXCLUDED.first_name_th,
			middle_name_th = EXCLUDED.middle_name_th,
			last_name_th   = EXCLUDED.last_name_th,
			first_name_en  = EXCLUDED.first_name_en,
			middle_name_en = EXCLUDED.middle_name_en,
			last_name_en   = EXCLUDED.last_name_en,
			date_of_birth  = EXCLUDED.date_of_birth,
			phone_number   = EXCLUDED.phone_number,
			email          = EXCLUDED.email,
			gender         = EXCLUDED.gender,
			updated_at     = now()`

	return pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, deleteStaleIdentityRowsQuery,
			patient.HospitalID, patient.PatientHN, nullIfEmpty(patient.NationalID), nullIfEmpty(patient.PassportID))
		if err != nil {
			return fmt.Errorf("patient repository: remove stale identity rows: %w", err)
		}

		_, err = tx.Exec(ctx, upsertQuery,
			patient.HospitalID, patient.PatientHN, nullIfEmpty(patient.NationalID), nullIfEmpty(patient.PassportID),
			nullIfEmpty(patient.FirstNameTH), nullIfEmpty(patient.MiddleNameTH), nullIfEmpty(patient.LastNameTH),
			nullIfEmpty(patient.FirstNameEN), nullIfEmpty(patient.MiddleNameEN), nullIfEmpty(patient.LastNameEN),
			patient.DateOfBirth, nullIfEmpty(patient.PhoneNumber), nullIfEmpty(patient.Email),
			nullIfEmpty(string(patient.Gender)),
		)
		if err != nil {
			return fmt.Errorf("patient repository: upsert: %w", err)
		}
		return nil
	})
}

// patientFilter is a parameterised WHERE clause; user input only ever travels in args.
type patientFilter struct {
	whereClause string
	args        []any
}

// buildPatientFilter turns search criteria into SQL conditions. Every query is scoped to one hospital.
func buildPatientFilter(hospitalID int64, criteria domain.PatientSearchCriteria) patientFilter {
	conditions := []string{"hospital_id = $1"}
	args := []any{hospitalID}

	// addCondition appends a value and a condition; %[1]d in the template is that value's placeholder number.
	addCondition := func(conditionTemplate string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(conditionTemplate, len(args)))
	}

	if criteria.NationalID != "" {
		addCondition("national_id = $%[1]d", criteria.NationalID)
	}
	if criteria.PassportID != "" {
		// Passport numbers are stored upper-cased (domain.NormalizePassportID), so a plain comparison can use the index.
		addCondition("passport_id = $%[1]d", domain.NormalizePassportID(criteria.PassportID))
	}
	if criteria.FirstName != "" {
		addCondition("(first_name_th ILIKE $%[1]d OR first_name_en ILIKE $%[1]d)", containsPattern(criteria.FirstName))
	}
	if criteria.MiddleName != "" {
		addCondition("(middle_name_th ILIKE $%[1]d OR middle_name_en ILIKE $%[1]d)", containsPattern(criteria.MiddleName))
	}
	if criteria.LastName != "" {
		addCondition("(last_name_th ILIKE $%[1]d OR last_name_en ILIKE $%[1]d)", containsPattern(criteria.LastName))
	}
	if criteria.DateOfBirth != nil {
		addCondition("date_of_birth = $%[1]d", *criteria.DateOfBirth)
	}
	if criteria.PhoneNumber != "" {
		addCondition("regexp_replace(phone_number, '[^0-9]', '', 'g') = ANY($%[1]d)", phoneNumberVariants(criteria.PhoneNumber))
	}
	if criteria.Email != "" {
		addCondition("lower(email) = lower($%[1]d::text)", criteria.Email)
	}

	return patientFilter{whereClause: strings.Join(conditions, " AND "), args: args}
}

// containsPattern builds an ILIKE pattern for "contains", escaping the LIKE wildcards in the user's text.
func containsPattern(text string) string {
	escaper := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + escaper.Replace(text) + "%"
}

// Thai phone numbers are written either nationally ("0812345678") or internationally ("+66812345678").
const (
	thaiTrunkPrefix   = "0"
	thaiCountryPrefix = "66"
)

// phoneNumberVariants returns the digits of a phone number in both its national and international
// Thai forms, so a search for "+66 80 000 0001" finds a record stored as "080-000-0001" and vice versa.
func phoneNumberVariants(phoneNumber string) []string {
	digits := digitsOnly(phoneNumber)
	variants := []string{digits}
	switch {
	case strings.HasPrefix(digits, thaiCountryPrefix) && len(digits) > len(thaiCountryPrefix)+1:
		variants = append(variants, thaiTrunkPrefix+strings.TrimPrefix(digits, thaiCountryPrefix))
	case strings.HasPrefix(digits, thaiTrunkPrefix) && len(digits) > 1:
		variants = append(variants, thaiCountryPrefix+strings.TrimPrefix(digits, thaiTrunkPrefix))
	}
	return variants
}

func digitsOnly(text string) string {
	var digits strings.Builder
	for _, character := range text {
		if character >= '0' && character <= '9' {
			digits.WriteRune(character)
		}
	}
	return digits.String()
}

func nullIfEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func scanPatient(row pgx.CollectableRow) (domain.Patient, error) {
	var (
		patient                               domain.Patient
		nationalID, passportID                *string
		firstNameTH, middleNameTH, lastNameTH *string
		firstNameEN, middleNameEN, lastNameEN *string
		dateOfBirth                           *time.Time
		phoneNumber, email, gender            *string
	)
	err := row.Scan(
		&patient.ID, &patient.HospitalID, &patient.PatientHN, &nationalID, &passportID,
		&firstNameTH, &middleNameTH, &lastNameTH, &firstNameEN, &middleNameEN, &lastNameEN,
		&dateOfBirth, &phoneNumber, &email, &gender, &patient.CreatedAt, &patient.UpdatedAt,
	)
	if err != nil {
		return domain.Patient{}, err
	}

	patient.NationalID = valueOrEmpty(nationalID)
	patient.PassportID = valueOrEmpty(passportID)
	patient.FirstNameTH = valueOrEmpty(firstNameTH)
	patient.MiddleNameTH = valueOrEmpty(middleNameTH)
	patient.LastNameTH = valueOrEmpty(lastNameTH)
	patient.FirstNameEN = valueOrEmpty(firstNameEN)
	patient.MiddleNameEN = valueOrEmpty(middleNameEN)
	patient.LastNameEN = valueOrEmpty(lastNameEN)
	patient.DateOfBirth = dateOfBirth
	patient.PhoneNumber = valueOrEmpty(phoneNumber)
	patient.Email = valueOrEmpty(email)
	patient.Gender = domain.Gender(valueOrEmpty(gender))
	return patient, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
