package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// sqlStateUniqueViolation is the PostgreSQL error code for a violated UNIQUE constraint.
const sqlStateUniqueViolation = "23505"

// StaffRepository stores and reads staff accounts in PostgreSQL.
type StaffRepository struct {
	db singleRowQuerier
}

// NewStaffRepository returns a repository that queries through pool.
func NewStaffRepository(pool *pgxpool.Pool) *StaffRepository {
	return &StaffRepository{db: pool}
}

// Create inserts a staff account and returns it with its generated id and creation time.
// The username must already be lowercase (the table enforces it). A username that already exists in the
// same hospital returns domain.ErrUsernameTaken; the unique constraint decides, so concurrent sign-ups cannot both win.
func (r *StaffRepository) Create(ctx context.Context, staff domain.Staff) (domain.Staff, error) {
	const query = `
		INSERT INTO staff (hospital_id, username, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, hospital_id, username, password_hash, created_at`

	var created domain.Staff
	err := r.db.QueryRow(ctx, query, staff.HospitalID, staff.Username, staff.PasswordHash).
		Scan(&created.ID, &created.HospitalID, &created.Username, &created.PasswordHash, &created.CreatedAt)

	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return created, nil
	case errors.As(err, &pgErr) && pgErr.Code == sqlStateUniqueViolation:
		return domain.Staff{}, domain.ErrUsernameTaken
	default:
		return domain.Staff{}, fmt.Errorf("staff repository: create: %w", err)
	}
}

// GetByHospitalAndUsername finds the staff account with the given username inside one hospital.
// It returns domain.ErrStaffNotFound when there is none.
func (r *StaffRepository) GetByHospitalAndUsername(ctx context.Context, hospitalID int64, username string) (domain.Staff, error) {
	const query = `
		SELECT id, hospital_id, username, password_hash, created_at
		FROM staff
		WHERE hospital_id = $1 AND username = $2`

	var staff domain.Staff
	err := r.db.QueryRow(ctx, query, hospitalID, username).
		Scan(&staff.ID, &staff.HospitalID, &staff.Username, &staff.PasswordHash, &staff.CreatedAt)
	switch {
	case err == nil:
		return staff, nil
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Staff{}, domain.ErrStaffNotFound
	default:
		return domain.Staff{}, fmt.Errorf("staff repository: get by hospital and username: %w", err)
	}
}
