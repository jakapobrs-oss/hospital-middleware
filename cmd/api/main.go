// Command api runs the hospital middleware HTTP service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/jakapobrs-oss/hospital-middleware/internal/auth"
	"github.com/jakapobrs-oss/hospital-middleware/internal/config"
	"github.com/jakapobrs-oss/hospital-middleware/internal/demodata"
	"github.com/jakapobrs-oss/hospital-middleware/internal/handler"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
	"github.com/jakapobrs-oss/hospital-middleware/internal/repository"
	"github.com/jakapobrs-oss/hospital-middleware/internal/router"
	"github.com/jakapobrs-oss/hospital-middleware/internal/service"
)

const (
	shutdownTimeout = 10 * time.Second
	seedTimeout     = 30 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	for _, warning := range cfg.Warnings() {
		logger.Warn("insecure configuration", "detail", warning)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := repository.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.RunMigrations {
		if err := repository.RunMigrations(cfg.DatabaseURL); err != nil {
			return err
		}
		logger.Info("database migrations applied")
	}

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           buildEngine(ctx, cfg, pool, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return serveUntilStopped(ctx, server, logger)
}

// buildEngine wires repositories, services and handlers into the HTTP engine.
func buildEngine(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	hospitalRepository := repository.NewHospitalRepository(pool)
	staffRepository := repository.NewStaffRepository(pool)
	patientRepository := repository.NewPatientRepository(pool)

	if cfg.SeedDemoData {
		// Demo data is a convenience: a seeding problem (or a locked table) is reported but must not keep the API down.
		seedCtx, cancel := context.WithTimeout(ctx, seedTimeout)
		if err := demodata.Seed(seedCtx, hospitalRepository, patientRepository); err != nil {
			logger.Warn("demo patients were not seeded", "error", err)
		} else {
			logger.Info("demo patients seeded")
		}
		cancel()
	}

	tokenManager := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL)
	staffService := service.NewStaffService(hospitalRepository, staffRepository,
		auth.NewPasswordHasher(bcrypt.DefaultCost), tokenManager, cfg.StaffRegistrationKey)
	patientService := service.NewPatientService(patientRepository,
		his.NewHTTPRegistry(cfg.HISBaseURLs, cfg.HISTimeout), logger)

	return router.New(router.Handlers{
		Health:  handler.NewHealthHandler(pool),
		Staff:   handler.NewStaffHandler(staffService),
		Patient: handler.NewPatientHandler(patientService, logger),
	}, tokenManager, logger)
}

// serveUntilStopped runs the server and shuts it down gracefully on SIGINT/SIGTERM.
func serveUntilStopped(ctx context.Context, server *http.Server, logger *slog.Logger) error {
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
