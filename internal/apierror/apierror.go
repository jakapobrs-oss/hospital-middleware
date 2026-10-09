// Package apierror writes every API error in one consistent JSON envelope:
//
//	{"error": {"code": "validation_error", "message": "...", "fields": [{"field": "password", "message": "..."}]}}
//
// Clients branch on the stable "code"; "message" is for humans and may change.
package apierror

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// Machine-readable error codes returned by the API.
const (
	CodeValidation             = "validation_error"
	CodeUnknownHospital        = "unknown_hospital"
	CodeUsernameTaken          = "username_taken"
	CodeInvalidCredentials     = "invalid_credentials"
	CodeInvalidRegistrationKey = "invalid_registration_key"
	CodeUnauthorized           = "unauthorized"
	CodeNotFound               = "not_found"
	CodePayloadTooLarge        = "payload_too_large"
	CodeInternal               = "internal_error"
)

// Body is the top-level error envelope.
type Body struct {
	Error Detail `json:"error"`
}

// Detail describes what went wrong.
type Detail struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Fields  []FieldError `json:"fields,omitempty"`
}

// FieldError points at one invalid request field, named as the client sent it (JSON or query name).
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Report validation errors with the client-facing field names ("first_name") instead of Go names ("FirstName").
func init() {
	if validate, ok := binding.Validator.Engine().(*validator.Validate); ok {
		validate.RegisterTagNameFunc(clientFieldName)
	}
}

func clientFieldName(field reflect.StructField) string {
	for _, tagKey := range []string{"json", "form"} {
		name, _, _ := strings.Cut(field.Tag.Get(tagKey), ",")
		if name != "" && name != "-" {
			return name
		}
	}
	return field.Name
}

// Respond aborts the request and writes an error envelope with the given status and code.
func Respond(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, Body{Error: Detail{Code: code, Message: message}})
}

// RespondFields aborts with 400 and explicit field errors, for checks that struct tags cannot express.
func RespondFields(c *gin.Context, fields ...FieldError) {
	c.AbortWithStatusJSON(http.StatusBadRequest, Body{Error: Detail{
		Code:    CodeValidation,
		Message: "request has invalid fields",
		Fields:  fields,
	}})
}

// IsBodyTooLarge reports whether err comes from reading past the request-body limit (http.MaxBytesReader).
func IsBodyTooLarge(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

// RespondBodyTooLarge aborts with 413 payload_too_large.
func RespondBodyTooLarge(c *gin.Context) {
	Respond(c, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body is too large")
}

// RespondValidation turns an error from ShouldBindJSON/ShouldBindQuery into a 400 response
// (or 413 when the body exceeded the size limit).
func RespondValidation(c *gin.Context, err error) {
	if IsBodyTooLarge(err) {
		RespondBodyTooLarge(c)
		return
	}
	var validationErrors validator.ValidationErrors
	if !errors.As(err, &validationErrors) {
		// Malformed JSON or a value of the wrong type: nothing field-specific to report.
		Respond(c, http.StatusBadRequest, CodeValidation, "request body or query string is malformed")
		return
	}

	fields := make([]FieldError, 0, len(validationErrors))
	for _, fieldErr := range validationErrors {
		fields = append(fields, FieldError{Field: fieldErr.Field(), Message: describe(fieldErr)})
	}
	RespondFields(c, fields...)
}

// describe converts a validator rule into a short human-readable message.
func describe(fieldErr validator.FieldError) string {
	unit := ""
	if fieldErr.Kind() == reflect.String {
		unit = " characters"
	}

	switch fieldErr.Tag() {
	case "required":
		return "is required"
	case "min":
		return "must be at least " + fieldErr.Param() + unit
	case "max":
		return "must be at most " + fieldErr.Param() + unit
	case "len":
		return "must be exactly " + fieldErr.Param() + unit
	case "email":
		return "must be a valid email address"
	case "numeric":
		return "must contain digits only"
	case "datetime":
		return "must be a date in YYYY-MM-DD format"
	case "oneof":
		return "must be one of: " + fieldErr.Param()
	default:
		return "is invalid (" + fieldErr.Tag() + ")"
	}
}
