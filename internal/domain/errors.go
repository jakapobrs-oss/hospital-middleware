package domain

import "errors"

// Sentinel errors shared across layers. Compare with errors.Is; wrap with fmt.Errorf("...: %w", err).
var (
	ErrHospitalNotFound   = errors.New("hospital not found")
	ErrStaffNotFound      = errors.New("staff not found")
	ErrUsernameTaken      = errors.New("username already exists in this hospital")
	ErrInvalidCredentials = errors.New("invalid username, password or hospital")
	// ErrInvalidRegistrationKey means staff registration is protected by a key and the request did not carry it.
	ErrInvalidRegistrationKey = errors.New("missing or invalid registration key")
	ErrInvalidToken           = errors.New("invalid or expired access token")
	ErrPatientNotFound        = errors.New("patient not found")
)
