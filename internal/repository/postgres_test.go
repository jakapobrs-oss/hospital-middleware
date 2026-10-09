package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPostgresPool_RejectsInvalidURL(t *testing.T) {
	_, err := NewPostgresPool(context.Background(), "not a url ::")

	assert.ErrorContains(t, err, "parse DATABASE_URL")
}

func TestRunMigrations_RejectsUnreachableDatabase(t *testing.T) {
	err := RunMigrations("postgres://user:pass@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")

	assert.ErrorContains(t, err, "migrations")
}
