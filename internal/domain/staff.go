package domain

import "time"

// Staff is a hospital employee who can log in and search patients of their own hospital.
type Staff struct {
	ID           int64
	HospitalID   int64
	Username     string // normalised to lowercase; unique within a hospital
	PasswordHash string // bcrypt hash, never the plain password
	CreatedAt    time.Time
}

// StaffIdentity is the authenticated staff member carried inside an access token.
// Patient queries are always scoped to HospitalID taken from here, never from the request.
type StaffIdentity struct {
	StaffID      int64
	HospitalID   int64
	HospitalCode string
	Username     string
}
