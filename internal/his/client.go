// Package his integrates the middleware with Hospital Information Systems (HIS).
//
// Every hospital exposes its own HIS. Client hides the wire format of one HIS behind a single method,
// and Registry picks the right client for the staff member's hospital.
package his

import (
	"context"
	"strings"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// Client looks up one patient in a hospital's HIS.
type Client interface {
	// FindPatient returns the patient whose national ID or passport ID equals identityNumber.
	// It returns domain.ErrPatientNotFound when the HIS has no such patient.
	// The returned patient has no HospitalID; the caller assigns it.
	FindPatient(ctx context.Context, identityNumber string) (domain.Patient, error)
}

// Registry maps a hospital code to the HIS client of that hospital.
type Registry struct {
	clientsByHospitalCode map[string]Client
}

// NewRegistry builds a registry from ready-made clients keyed by hospital code.
func NewRegistry(clientsByHospitalCode map[string]Client) *Registry {
	normalized := make(map[string]Client, len(clientsByHospitalCode))
	for hospitalCode, client := range clientsByHospitalCode {
		normalized[strings.ToLower(hospitalCode)] = client
	}
	return &Registry{clientsByHospitalCode: normalized}
}

// ClientFor returns the HIS client of a hospital; found is false when the hospital has no HIS integration.
func (registry *Registry) ClientFor(hospitalCode string) (client Client, found bool) {
	client, found = registry.clientsByHospitalCode[strings.ToLower(hospitalCode)]
	return client, found
}
