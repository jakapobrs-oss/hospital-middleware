//go:build integration

package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/repository"
)

// The SQL of FindByCodeOrName (a CTE that drops ambiguous names) cannot be checked with fakes, so it runs here.
func TestIntegration_HospitalRepository_FindByCodeOrName(t *testing.T) {
	database := setupDatabase(t)
	ctx := context.Background()
	hospitals := repository.NewHospitalRepository(database.pool)

	t.Run("finds a hospital however its code or name is written", func(t *testing.T) {
		references := []string{
			"hospital-a", "HOSPITAL-A", "Hospital-A", "Hospital A", "hospital a", "hospital_a", "  Hospital   A  ", "Hospital\tA",
		}
		for _, reference := range references {
			hospital, err := hospitals.FindByCodeOrName(ctx, reference)

			require.NoError(t, err, "reference %q", reference)
			assert.Equal(t, database.hospitalA, hospital, "reference %q", reference)
		}
	})

	t.Run("keeps the hospitals apart", func(t *testing.T) {
		hospital, err := hospitals.FindByCodeOrName(ctx, "Hospital B")

		require.NoError(t, err)
		assert.Equal(t, database.hospitalB, hospital)
	})

	t.Run("reports ErrHospitalNotFound when nothing matches", func(t *testing.T) {
		for _, reference := range []string{"hospital-z", "hospital", "A", "", "   ", "hospital-a-extra", "%", "hospital-%"} {
			_, err := hospitals.FindByCodeOrName(ctx, reference)

			assert.ErrorIs(t, err, domain.ErrHospitalNotFound, "reference %q", reference)
		}
	})

	t.Run("a code match wins over a hospital whose name looks like that code", func(t *testing.T) {
		insertHospitals(t, database, [][2]string{{"look-alike", "Hospital-A"}})

		hospital, err := hospitals.FindByCodeOrName(ctx, "hospital-a")

		require.NoError(t, err)
		assert.Equal(t, database.hospitalA, hospital, "the real code must win, not the look-alike's name")
	})

	t.Run("a name shared by two hospitals matches neither, but their codes still work", func(t *testing.T) {
		t.Cleanup(func() { deleteHospitals(database, "twin-1", "twin-2") })
		if err := tryInsertHospitals(database, [][2]string{{"twin-1", "Twin Clinic"}, {"twin-2", "Twin Clinic"}}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				t.Skip("this schema already forbids two hospitals with the same name")
			}
			require.NoError(t, err)
		}

		_, err := hospitals.FindByCodeOrName(ctx, "Twin Clinic")
		assert.ErrorIs(t, err, domain.ErrHospitalNotFound, "an ambiguous name must never pick a hospital")

		first, err := hospitals.FindByCodeOrName(ctx, "twin-1")
		require.NoError(t, err)
		assert.Equal(t, "twin-1", first.Code)
	})
}

func insertHospitals(t *testing.T, database testDatabase, codesAndNames [][2]string) {
	t.Helper()
	codes := make([]string, 0, len(codesAndNames))
	for _, codeAndName := range codesAndNames {
		codes = append(codes, codeAndName[0])
	}
	t.Cleanup(func() { deleteHospitals(database, codes...) })
	require.NoError(t, tryInsertHospitals(database, codesAndNames))
}

func tryInsertHospitals(database testDatabase, codesAndNames [][2]string) error {
	for _, codeAndName := range codesAndNames {
		if _, err := database.pool.Exec(context.Background(),
			"INSERT INTO hospitals (code, name) VALUES ($1, $2)", codeAndName[0], codeAndName[1]); err != nil {
			return err
		}
	}
	return nil
}

func deleteHospitals(database testDatabase, codes ...string) {
	_, _ = database.pool.Exec(context.Background(), "DELETE FROM hospitals WHERE code = ANY($1)", codes)
}
