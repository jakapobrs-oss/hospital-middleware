// Package handler contains the Gin HTTP handlers: request binding, validation and response DTOs.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const healthCheckTimeout = 2 * time.Second

// DatabasePinger reports whether the database is reachable.
type DatabasePinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler serves GET /health for container health checks.
type HealthHandler struct {
	database DatabasePinger
}

// NewHealthHandler creates a HealthHandler.
func NewHealthHandler(database DatabasePinger) *HealthHandler {
	return &HealthHandler{database: database}
}

// Check answers 200 when the database is reachable, otherwise 503.
func (handler *HealthHandler) Check(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), healthCheckTimeout)
	defer cancel()

	if err := handler.database.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
