package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
	"github.com/jakapobrs-oss/hospital-middleware/internal/auth"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/service"
)

const (
	accessTokenType = "Bearer"

	// registrationKeyHeader carries the optional key that protects POST /staff/create (see StaffService).
	registrationKeyHeader = "X-Registration-Key"
)

// usernamePattern allows letters, digits and . _ @ + - : enough for names and e-mail addresses, and safe in
// URLs and logs. Usernames are stored lowercase, so the pattern does not need to care about case.
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._@+-]+$`)

// StaffService is what the staff handlers need from the business layer.
type StaffService interface {
	Create(ctx context.Context, input service.CreateStaffInput) (service.CreatedStaff, error)
	Login(ctx context.Context, input service.LoginInput) (service.LoginResult, error)
}

// StaffHandler serves the staff onboarding and login endpoints.
type StaffHandler struct {
	staffService StaffService
	now          func() time.Time // replaced in tests so expires_in is predictable
}

// NewStaffHandler returns a handler that delegates to staffService.
func NewStaffHandler(staffService StaffService) *StaffHandler {
	return &StaffHandler{staffService: staffService, now: time.Now}
}

// createStaffRequest is the JSON body of POST /staff/create. Hospital is the hospital's code or name.
type createStaffRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Password string `json:"password" binding:"required,min=8"`
	Hospital string `json:"hospital" binding:"required,max=100"`
}

// formatErrors reports the rules the struct tags cannot express: the username alphabet and the password
// limit in bytes (a 25-character Thai password is already 75 bytes, over what bcrypt can use).
func (req createStaffRequest) formatErrors() []apierror.FieldError {
	var fieldErrors []apierror.FieldError
	if !usernamePattern.MatchString(req.Username) {
		fieldErrors = append(fieldErrors, apierror.FieldError{
			Field:   "username",
			Message: "may contain only letters, digits and the characters . _ @ + -",
		})
	}
	if len(req.Password) > auth.MaxPasswordBytes {
		fieldErrors = append(fieldErrors, apierror.FieldError{
			Field:   "password",
			Message: fmt.Sprintf("must be at most %d bytes", auth.MaxPasswordBytes),
		})
	}
	return append(fieldErrors, nulCharacterErrors(map[string]string{"hospital": req.Hospital, "password": req.Password})...)
}

// nulCharacterErrors reports fields containing U+0000. PostgreSQL text cannot hold it, so such a value
// would otherwise reach the database and fail there as a 500 instead of a 400. Passwords are only ever
// hashed, but they follow the same rule so every endpoint treats NUL the same way.
func nulCharacterErrors(valuesByField map[string]string) []apierror.FieldError {
	var fieldErrors []apierror.FieldError
	for _, field := range []string{"username", "password", "hospital"} {
		if value, present := valuesByField[field]; present && strings.ContainsRune(value, 0) {
			fieldErrors = append(fieldErrors, apierror.FieldError{Field: field, Message: "must not contain NUL characters"})
		}
	}
	return fieldErrors
}

// loginRequest is the JSON body of POST /staff/login. It only caps lengths: the real password policy
// is deliberately not revealed on this endpoint, so a short password is simply a failed login.
// Hospital is the hospital's code or name.
type loginRequest struct {
	Username string `json:"username" binding:"required,max=50"`
	Password string `json:"password" binding:"required,max=128"`
	Hospital string `json:"hospital" binding:"required,max=100"`
}

type hospitalSummary struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type createStaffResponse struct {
	ID        int64           `json:"id"`
	Username  string          `json:"username"`
	Hospital  hospitalSummary `json:"hospital"`
	CreatedAt string          `json:"created_at"`
}

type staffSummary struct {
	ID       int64           `json:"id"`
	Username string          `json:"username"`
	Hospital hospitalSummary `json:"hospital"`
}

type loginResponse struct {
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int64        `json:"expires_in"`
	Staff       staffSummary `json:"staff"`
}

// Create handles POST /staff/create. When registration is protected, the X-Registration-Key header must carry the key.
func (h *StaffHandler) Create(c *gin.Context) {
	var req createStaffRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.RespondValidation(c, err)
		return
	}
	if fieldErrors := req.formatErrors(); len(fieldErrors) > 0 {
		apierror.RespondFields(c, fieldErrors...)
		return
	}

	created, err := h.staffService.Create(c.Request.Context(), service.CreateStaffInput{
		Username:        req.Username,
		Password:        req.Password,
		HospitalCode:    req.Hospital,
		RegistrationKey: c.GetHeader(registrationKeyHeader),
	})
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, createStaffResponse{
			ID:        created.Staff.ID,
			Username:  created.Staff.Username,
			Hospital:  hospitalSummary{Code: created.Hospital.Code, Name: created.Hospital.Name},
			CreatedAt: created.Staff.CreatedAt.UTC().Format(time.RFC3339),
		})
	case errors.Is(err, domain.ErrInvalidRegistrationKey):
		apierror.Respond(c, http.StatusForbidden, apierror.CodeInvalidRegistrationKey,
			"staff registration needs a valid "+registrationKeyHeader+" header")
	case errors.Is(err, domain.ErrHospitalNotFound):
		apierror.Respond(c, http.StatusBadRequest, apierror.CodeUnknownHospital,
			"hospital not found: use its code (for example hospital-a) or its name")
	case errors.Is(err, domain.ErrUsernameTaken):
		apierror.Respond(c, http.StatusConflict, apierror.CodeUsernameTaken, "username already exists in this hospital")
	default:
		respondStaffInternalError(c, "create staff", err)
	}
}

// Login handles POST /staff/login.
func (h *StaffHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.RespondValidation(c, err)
		return
	}
	if fieldErrors := nulCharacterErrors(map[string]string{"username": req.Username, "password": req.Password, "hospital": req.Hospital}); len(fieldErrors) > 0 {
		apierror.RespondFields(c, fieldErrors...)
		return
	}

	result, err := h.staffService.Login(c.Request.Context(), service.LoginInput{
		Username:     req.Username,
		Password:     req.Password,
		HospitalCode: req.Hospital,
	})
	switch {
	case err == nil:
		// The body carries a credential, so browsers and proxies must not keep a copy.
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, loginResponse{
			AccessToken: result.AccessToken,
			TokenType:   accessTokenType,
			ExpiresIn:   h.secondsUntil(result.ExpiresAt),
			Staff: staffSummary{
				ID:       result.Staff.ID,
				Username: result.Staff.Username,
				Hospital: hospitalSummary{Code: result.Hospital.Code, Name: result.Hospital.Name},
			},
		})
	case errors.Is(err, domain.ErrInvalidCredentials):
		apierror.Respond(c, http.StatusUnauthorized, apierror.CodeInvalidCredentials, "invalid username, password or hospital")
	default:
		respondStaffInternalError(c, "log in staff", err)
	}
}

// secondsUntil returns the whole seconds left until expiresAt, never negative. Rounding hides the few
// milliseconds between signing the token and answering the request, so a 1 h token reports 3600.
func (h *StaffHandler) secondsUntil(expiresAt time.Time) int64 {
	remaining := expiresAt.Sub(h.now()).Round(time.Second)
	if remaining < 0 {
		return 0
	}
	return int64(remaining / time.Second)
}

// respondStaffInternalError logs the real cause and sends a generic 500, so internals never reach the client.
// The error comes from the service and repository layers, which never put passwords, hashes or tokens in it.
func respondStaffInternalError(c *gin.Context, operation string, err error) {
	slog.ErrorContext(c.Request.Context(), "staff handler: request failed", "operation", operation, "error", err)
	apierror.Respond(c, http.StatusInternalServerError, apierror.CodeInternal, "internal server error")
}
