// Package domain holds the core business entities and errors shared by every layer.
// It has no dependencies on HTTP, the database or any third-party library.
package domain

// Hospital is a tenant of the middleware. Staff and patients always belong to exactly one hospital.
type Hospital struct {
	ID   int64
	Code string // stable lowercase identifier used by API clients, e.g. "hospital-a"
	Name string
}
