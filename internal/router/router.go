// Package router declares the HTTP routes of the middleware.
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
	"github.com/jakapobrs-oss/hospital-middleware/internal/handler"
	"github.com/jakapobrs-oss/hospital-middleware/internal/middleware"
)

const (
	healthPath = "/health"

	// maxRequestBodyBytes caps every request body; the largest legitimate body is a few hundred bytes.
	maxRequestBodyBytes = 1 << 20
)

// Handlers groups the handlers the routes dispatch to.
type Handlers struct {
	Health  *handler.HealthHandler
	Staff   *handler.StaffHandler
	Patient *handler.PatientHandler
}

// New builds the Gin engine with every route of the API.
func New(handlers Handlers, tokenVerifier middleware.TokenVerifier, logger *slog.Logger) *gin.Engine {
	engine := gin.New()
	// The logger runs first so a request that panics is still logged once, after recovery answered it.
	engine.Use(requestLogger(logger), recoverWithoutRequestDump(logger), limitRequestBody(maxRequestBodyBytes))
	// Nginx is the only client of the service; client IPs are not used for decisions here.
	_ = engine.SetTrustedProxies(nil)

	engine.GET(healthPath, handlers.Health.Check)

	staffRoutes := engine.Group("/staff")
	staffRoutes.POST("/create", handlers.Staff.Create)
	staffRoutes.POST("/login", handlers.Staff.Login)

	patientRoutes := engine.Group("/patient", middleware.RequireAuth(tokenVerifier))
	patientRoutes.GET("/search", handlers.Patient.Search)
	patientRoutes.POST("/search", handlers.Patient.Search)
	patientRoutes.GET("/search/:id", handlers.Patient.FindByIdentityNumber)

	engine.NoRoute(func(c *gin.Context) {
		apierror.Respond(c, http.StatusNotFound, apierror.CodeNotFound, "route not found")
	})
	return engine
}

// requestLogger logs one line per request. It records the route template ("/patient/search/:id"),
// never the raw URL: national IDs travel in the query of GET /patient/search and in the path of
// GET /patient/search/{id}. Health checks run every few seconds and would bury real traffic, so
// they are not logged.
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == healthPath {
			c.Next()
			return
		}
		startedAt := time.Now()
		c.Next()
		logger.InfoContext(c.Request.Context(), "http request",
			"method", c.Request.Method,
			"route", routeTemplate(c),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}
}

// routeTemplate is the matched route pattern, or "unmatched" for a 404 (whose raw path could hold anything).
func routeTemplate(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "unmatched"
}

// recoverWithoutRequestDump turns a panic into a 500. Gin's default recovery prints the raw request,
// whose URL can hold a national ID, so only the method, route template and panic value are logged here.
func recoverWithoutRequestDump(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(c.Request.Context(), "panic while handling request",
					"method", c.Request.Method, "route", routeTemplate(c), "panic", recovered)
				apierror.Respond(c, http.StatusInternalServerError, apierror.CodeInternal, "internal server error")
			}
		}()
		c.Next()
	}
}

// limitRequestBody stops a client from making the service read an arbitrarily large body.
func limitRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
