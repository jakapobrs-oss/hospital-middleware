package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

var storedStaff = domain.Staff{
	ID:           42,
	HospitalID:   1,
	Username:     "nurse01",
	PasswordHash: "$2a$10$stored-hash",
	CreatedAt:    time.Date(2026, 10, 10, 3, 15, 0, 0, time.UTC),
}

// staffRow returns a row whose Scan fills the five staff columns in the order the queries select them.
// The type assertions double as a check that the repository scans into fields of the right types.
func staffRow(staff domain.Staff) pgx.Row {
	return stubRow{scan: func(dest ...any) error {
		*dest[0].(*int64) = staff.ID
		*dest[1].(*int64) = staff.HospitalID
		*dest[2].(*string) = staff.Username
		*dest[3].(*string) = staff.PasswordHash
		*dest[4].(*time.Time) = staff.CreatedAt
		return nil
	}}
}

func TestStaffRepository_Create(t *testing.T) {
	newStaff := domain.Staff{HospitalID: 1, Username: "nurse01", PasswordHash: "$2a$10$stored-hash"}

	t.Run("returns the stored account with its generated id", func(t *testing.T) {
		querier := &stubSingleRowQuerier{row: staffRow(storedStaff)}
		repo := &StaffRepository{db: querier}

		created, err := repo.Create(context.Background(), newStaff)

		require.NoError(t, err)
		assert.Equal(t, storedStaff, created)
		assert.Contains(t, querier.gotSQL, "INSERT INTO staff")
		assert.Equal(t, []any{int64(1), "nurse01", "$2a$10$stored-hash"}, querier.gotArgs)
	})

	t.Run("maps a unique violation to ErrUsernameTaken", func(t *testing.T) {
		uniqueViolation := &pgconn.PgError{Code: "23505", ConstraintName: "uq_staff_hospital_username"}
		repo := &StaffRepository{db: &stubSingleRowQuerier{row: failingRow(uniqueViolation)}}

		created, err := repo.Create(context.Background(), newStaff)

		assert.ErrorIs(t, err, domain.ErrUsernameTaken)
		assert.Equal(t, domain.Staff{}, created)
	})

	t.Run("recognises a unique violation that another layer wrapped", func(t *testing.T) {
		wrapped := fmt.Errorf("driver: %w", &pgconn.PgError{Code: "23505"})
		repo := &StaffRepository{db: &stubSingleRowQuerier{row: failingRow(wrapped)}}

		_, err := repo.Create(context.Background(), newStaff)

		assert.ErrorIs(t, err, domain.ErrUsernameTaken)
	})

	t.Run("does not treat other database errors as a taken username", func(t *testing.T) {
		tests := []struct {
			name        string
			databaseErr error
		}{
			{name: "foreign key violation", databaseErr: &pgconn.PgError{Code: "23503"}},
			{name: "connection failure", databaseErr: errors.New("connection reset by peer")},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repo := &StaffRepository{db: &stubSingleRowQuerier{row: failingRow(tc.databaseErr)}}

				_, err := repo.Create(context.Background(), newStaff)

				assert.ErrorIs(t, err, tc.databaseErr)
				assert.NotErrorIs(t, err, domain.ErrUsernameTaken)
			})
		}
	})
}

func TestStaffRepository_GetByHospitalAndUsername(t *testing.T) {
	t.Run("returns the account of that hospital", func(t *testing.T) {
		querier := &stubSingleRowQuerier{row: staffRow(storedStaff)}
		repo := &StaffRepository{db: querier}

		staff, err := repo.GetByHospitalAndUsername(context.Background(), 1, "nurse01")

		require.NoError(t, err)
		assert.Equal(t, storedStaff, staff)
		assert.Contains(t, querier.gotSQL, "FROM staff")
		assert.Equal(t, []any{int64(1), "nurse01"}, querier.gotArgs)
	})

	t.Run("maps a missing row to ErrStaffNotFound", func(t *testing.T) {
		repo := &StaffRepository{db: &stubSingleRowQuerier{row: failingRow(pgx.ErrNoRows)}}

		staff, err := repo.GetByHospitalAndUsername(context.Background(), 1, "ghost")

		assert.ErrorIs(t, err, domain.ErrStaffNotFound)
		assert.Equal(t, domain.Staff{}, staff)
	})

	t.Run("wraps any other database error", func(t *testing.T) {
		databaseErr := errors.New("connection reset by peer")
		repo := &StaffRepository{db: &stubSingleRowQuerier{row: failingRow(databaseErr)}}

		_, err := repo.GetByHospitalAndUsername(context.Background(), 1, "nurse01")

		assert.ErrorIs(t, err, databaseErr)
		assert.NotErrorIs(t, err, domain.ErrStaffNotFound)
	})
}
