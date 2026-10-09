package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// singleRowQuerier is the one pgx pool method the hospital and staff lookups need.
// Declaring it here lets unit tests swap the pool for a fake and check the error mapping without a database.
type singleRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// codeSeparators matches the characters people type instead of the hyphens of a hospital code.
var codeSeparators = regexp.MustCompile(`[\s_]+`)

// HospitalRepository reads hospitals from PostgreSQL.
type HospitalRepository struct {
	db singleRowQuerier
}

// NewHospitalRepository returns a repository that queries through pool.
func NewHospitalRepository(pool *pgxpool.Pool) *HospitalRepository {
	return &HospitalRepository{db: pool}
}

// GetByCode finds a hospital by its lowercase code, for example "hospital-a".
// It returns domain.ErrHospitalNotFound when no hospital has that code.
func (r *HospitalRepository) GetByCode(ctx context.Context, code string) (domain.Hospital, error) {
	const query = `SELECT id, code, name FROM hospitals WHERE code = $1`

	var hospital domain.Hospital
	err := r.db.QueryRow(ctx, query, code).Scan(&hospital.ID, &hospital.Code, &hospital.Name)
	switch {
	case err == nil:
		return hospital, nil
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Hospital{}, domain.ErrHospitalNotFound
	default:
		return domain.Hospital{}, fmt.Errorf("hospital repository: get by code: %w", err)
	}
}

// FindByCodeOrName finds a hospital from what a person typed: its code ("hospital-a") or its name ("Hospital A"),
// in any letter case, with spaces or underscores accepted where a code has hyphens ("hospital_a").
//
// A code match always wins. A name shared by several hospitals matches none of them, so a request can never
// land in the wrong hospital. It returns domain.ErrHospitalNotFound when nothing matches.
func (r *HospitalRepository) FindByCodeOrName(ctx context.Context, reference string) (domain.Hospital, error) {
	// $1 is the reference as a lower-case name, $2 the same text as a code. The row count is taken before the
	// final filter, so two hospitals with the same name are both dropped unless one of them matches by code.
	const query = `
		WITH matches AS (
			SELECT id, code, name, (code = $2) AS by_code
			FROM hospitals
			WHERE code = $2 OR lower(name) = $1
		)
		SELECT id, code, name
		FROM matches
		WHERE by_code OR (SELECT count(*) FROM matches) = 1
		ORDER BY by_code DESC
		LIMIT 1`

	var hospital domain.Hospital
	err := r.db.QueryRow(ctx, query, hospitalNameForm(reference), hospitalCodeForm(reference)).
		Scan(&hospital.ID, &hospital.Code, &hospital.Name)
	switch {
	case err == nil:
		return hospital, nil
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Hospital{}, domain.ErrHospitalNotFound
	default:
		return domain.Hospital{}, fmt.Errorf("hospital repository: find by code or name: %w", err)
	}
}

// hospitalCodeForm turns what a person typed into the stored code form: "Hospital A" and "hospital_a"
// both become "hospital-a".
func hospitalCodeForm(reference string) string {
	return codeSeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(reference)), "-")
}

// hospitalNameForm lower-cases the reference and collapses runs of white space, so it can be compared
// with lower(name).
func hospitalNameForm(reference string) string {
	return strings.Join(strings.Fields(strings.ToLower(reference)), " ")
}
