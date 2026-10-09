// Package repository persists domain entities in PostgreSQL using pgx.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the "pgx5" migration driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jakapobrs-oss/hospital-middleware/migrations"
)

const (
	connectRetryInterval = time.Second
	connectRetryTimeout  = 30 * time.Second
)

// NewPostgresPool opens a connection pool and waits until the database answers a ping,
// so the service survives starting a few seconds before PostgreSQL is ready.
func NewPostgresPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DATABASE_URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	deadline := time.Now().Add(connectRetryTimeout)
	for {
		pingErr := pool.Ping(ctx)
		if pingErr == nil {
			return pool, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("postgres: database not reachable after %s: %w", connectRetryTimeout, pingErr)
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(connectRetryInterval):
		}
	}
}

// RunMigrations applies every pending migration embedded in the binary.
func RunMigrations(databaseURL string) error {
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("migrations: open embedded files: %w", err)
	}
	migrator, err := migrate.NewWithSourceInstance("iofs", source, toMigrateURL(databaseURL))
	if err != nil {
		return fmt.Errorf("migrations: connect: %w", err)
	}
	defer migrator.Close()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: apply: %w", err)
	}
	return nil
}

// toMigrateURL switches a postgres:// URL to the pgx5:// scheme expected by golang-migrate's pgx v5 driver.
func toMigrateURL(databaseURL string) string {
	for _, scheme := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(databaseURL, scheme) {
			return "pgx5://" + strings.TrimPrefix(databaseURL, scheme)
		}
	}
	return databaseURL
}
