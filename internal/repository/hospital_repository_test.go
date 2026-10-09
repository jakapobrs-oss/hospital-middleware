package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// stubRow is a pgx.Row whose Scan behaviour is supplied by the test.
type stubRow struct {
	scan func(dest ...any) error
}

func (row stubRow) Scan(dest ...any) error {
	return row.scan(dest...)
}

// failingRow returns a row whose Scan fails with err, like a query that found nothing or hit a database error.
func failingRow(err error) pgx.Row {
	return stubRow{scan: func(...any) error { return err }}
}

// stubSingleRowQuerier hands back a fixed row and remembers the query it was asked to run.
type stubSingleRowQuerier struct {
	row     pgx.Row
	gotSQL  string
	gotArgs []any
}

func (q *stubSingleRowQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.gotSQL = sql
	q.gotArgs = args
	return q.row
}

func TestHospitalRepository_GetByCode(t *testing.T) {
	t.Run("returns the hospital stored under the code", func(t *testing.T) {
		querier := &stubSingleRowQuerier{row: stubRow{scan: func(dest ...any) error {
			*dest[0].(*int64) = 2
			*dest[1].(*string) = "hospital-b"
			*dest[2].(*string) = "Hospital B"
			return nil
		}}}
		repo := &HospitalRepository{db: querier}

		hospital, err := repo.GetByCode(context.Background(), "hospital-b")

		require.NoError(t, err)
		assert.Equal(t, domain.Hospital{ID: 2, Code: "hospital-b", Name: "Hospital B"}, hospital)
		assert.Contains(t, querier.gotSQL, "FROM hospitals")
		assert.Equal(t, []any{"hospital-b"}, querier.gotArgs)
	})

	t.Run("maps a missing row to ErrHospitalNotFound", func(t *testing.T) {
		repo := &HospitalRepository{db: &stubSingleRowQuerier{row: failingRow(pgx.ErrNoRows)}}

		hospital, err := repo.GetByCode(context.Background(), "hospital-z")

		assert.ErrorIs(t, err, domain.ErrHospitalNotFound)
		assert.Equal(t, domain.Hospital{}, hospital)
	})

	t.Run("wraps any other database error", func(t *testing.T) {
		databaseErr := errors.New("connection reset by peer")
		repo := &HospitalRepository{db: &stubSingleRowQuerier{row: failingRow(databaseErr)}}

		_, err := repo.GetByCode(context.Background(), "hospital-a")

		assert.ErrorIs(t, err, databaseErr)
		assert.NotErrorIs(t, err, domain.ErrHospitalNotFound)
	})
}

func TestHospitalRepository_FindByCodeOrName(t *testing.T) {
	t.Run("returns the hospital and queries with the name form and the code form of the reference", func(t *testing.T) {
		querier := &stubSingleRowQuerier{row: stubRow{scan: func(dest ...any) error {
			*dest[0].(*int64) = 1
			*dest[1].(*string) = "hospital-a"
			*dest[2].(*string) = "Hospital A"
			return nil
		}}}
		repo := &HospitalRepository{db: querier}

		hospital, err := repo.FindByCodeOrName(context.Background(), "Hospital_A")

		require.NoError(t, err)
		assert.Equal(t, domain.Hospital{ID: 1, Code: "hospital-a", Name: "Hospital A"}, hospital)
		assert.Contains(t, querier.gotSQL, "FROM hospitals")
		assert.Equal(t, []any{"hospital_a", "hospital-a"}, querier.gotArgs, "name form first, code form second")
	})

	t.Run("maps no match, or an ambiguous name, to ErrHospitalNotFound", func(t *testing.T) {
		repo := &HospitalRepository{db: &stubSingleRowQuerier{row: failingRow(pgx.ErrNoRows)}}

		hospital, err := repo.FindByCodeOrName(context.Background(), "nowhere")

		assert.ErrorIs(t, err, domain.ErrHospitalNotFound)
		assert.Equal(t, domain.Hospital{}, hospital)
	})

	t.Run("wraps any other database error", func(t *testing.T) {
		databaseErr := errors.New("connection reset by peer")
		repo := &HospitalRepository{db: &stubSingleRowQuerier{row: failingRow(databaseErr)}}

		_, err := repo.FindByCodeOrName(context.Background(), "hospital-a")

		assert.ErrorIs(t, err, databaseErr)
		assert.NotErrorIs(t, err, domain.ErrHospitalNotFound)
	})
}

func TestHospitalReferenceForms(t *testing.T) {
	tests := []struct {
		reference string
		wantCode  string
		wantName  string
	}{
		{reference: "hospital-a", wantCode: "hospital-a", wantName: "hospital-a"},
		{reference: "Hospital A", wantCode: "hospital-a", wantName: "hospital a"},
		{reference: "HOSPITAL-A", wantCode: "hospital-a", wantName: "hospital-a"},
		{reference: "hospital_a", wantCode: "hospital-a", wantName: "hospital_a"},
		{reference: "  Hospital   A  ", wantCode: "hospital-a", wantName: "hospital a"},
		{reference: "Hospital\tA", wantCode: "hospital-a", wantName: "hospital a"},
		{reference: "Bangkok_General  Hospital", wantCode: "bangkok-general-hospital", wantName: "bangkok_general hospital"},
		{reference: "", wantCode: "", wantName: ""},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.reference), func(t *testing.T) {
			assert.Equal(t, tc.wantCode, hospitalCodeForm(tc.reference), "code form")
			assert.Equal(t, tc.wantName, hospitalNameForm(tc.reference), "name form")
		})
	}
}
