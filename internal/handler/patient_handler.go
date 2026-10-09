package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/middleware"
)

const (
	dateLayout = "2006-01-02"

	// maxIdentityNumberLength caps the {id} of GET /patient/search/{id}; real IDs are far shorter.
	maxIdentityNumberLength = 20

	unknownFieldMessage = "is not a supported search field"
)

// searchFieldNames lists every field /patient/search accepts. Anything else is rejected with 400:
// a misspelt filter that was silently ignored would return far more patient records than asked for.
var searchFieldNames = map[string]bool{
	"national_id": true, "passport_id": true, "first_name": true, "middle_name": true, "last_name": true,
	"date_of_birth": true, "phone_number": true, "email": true, "limit": true, "offset": true,
}

// PatientService is what the patient handler needs from the service layer.
type PatientService interface {
	Search(ctx context.Context, staff domain.StaffIdentity, criteria domain.PatientSearchCriteria) (domain.PatientSearchResult, error)
	FindByIdentityNumber(ctx context.Context, staff domain.StaffIdentity, identityNumber string) (domain.Patient, error)
}

// PatientHandler serves the patient search endpoints.
type PatientHandler struct {
	patientService PatientService
	logger         *slog.Logger
}

// NewPatientHandler creates a PatientHandler.
func NewPatientHandler(patientService PatientService, logger *slog.Logger) *PatientHandler {
	return &PatientHandler{patientService: patientService, logger: logger}
}

// searchPatientsRequest is read from query parameters and/or a JSON body; body values win.
type searchPatientsRequest struct {
	NationalID  string `json:"national_id" form:"national_id" binding:"max=20"`
	PassportID  string `json:"passport_id" form:"passport_id" binding:"max=20"`
	FirstName   string `json:"first_name" form:"first_name" binding:"max=100"`
	MiddleName  string `json:"middle_name" form:"middle_name" binding:"max=100"`
	LastName    string `json:"last_name" form:"last_name" binding:"max=100"`
	DateOfBirth string `json:"date_of_birth" form:"date_of_birth" binding:"omitempty,datetime=2006-01-02"`
	PhoneNumber string `json:"phone_number" form:"phone_number" binding:"max=20"`
	Email       string `json:"email" form:"email" binding:"max=254"`
	Limit       *int   `json:"limit" form:"limit" binding:"omitempty,min=1,max=100"`
	Offset      *int   `json:"offset" form:"offset" binding:"omitempty,min=0"`
}

type patientResponse struct {
	FirstNameTH  *string `json:"first_name_th"`
	MiddleNameTH *string `json:"middle_name_th"`
	LastNameTH   *string `json:"last_name_th"`
	FirstNameEN  *string `json:"first_name_en"`
	MiddleNameEN *string `json:"middle_name_en"`
	LastNameEN   *string `json:"last_name_en"`
	DateOfBirth  *string `json:"date_of_birth"`
	PatientHN    string  `json:"patient_hn"`
	NationalID   *string `json:"national_id"`
	PassportID   *string `json:"passport_id"`
	PhoneNumber  *string `json:"phone_number"`
	Email        *string `json:"email"`
	Gender       *string `json:"gender"`
}

type paginationResponse struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type searchPatientsResponse struct {
	Data       []patientResponse  `json:"data"`
	Pagination paginationResponse `json:"pagination"`
}

// Search handles GET and POST /patient/search. The route must be protected by middleware.RequireAuth.
func (handler *PatientHandler) Search(c *gin.Context) {
	staff, authenticated := middleware.StaffIdentityFrom(c)
	if !authenticated {
		// Fail closed if the route was ever registered without the auth middleware.
		apierror.Respond(c, http.StatusUnauthorized, apierror.CodeUnauthorized, "missing or invalid bearer token")
		return
	}

	request, ok := bindSearchRequest(c)
	if !ok {
		return
	}
	criteria, fieldErrors := request.toCriteria()
	if len(fieldErrors) > 0 {
		apierror.RespondFields(c, fieldErrors...)
		return
	}

	result, err := handler.patientService.Search(c.Request.Context(), staff, criteria)
	if err != nil {
		handler.logger.ErrorContext(c.Request.Context(), "patient search failed", "error", err)
		apierror.Respond(c, http.StatusInternalServerError, apierror.CodeInternal, "could not search patients")
		return
	}
	c.JSON(http.StatusOK, newSearchPatientsResponse(result))
}

// FindByIdentityNumber handles GET /patient/search/{id}, the same shape as the HIS route:
// {id} is a national ID or passport ID, and the response is that one patient.
func (handler *PatientHandler) FindByIdentityNumber(c *gin.Context) {
	staff, authenticated := middleware.StaffIdentityFrom(c)
	if !authenticated {
		apierror.Respond(c, http.StatusUnauthorized, apierror.CodeUnauthorized, "missing or invalid bearer token")
		return
	}

	identityNumber := strings.TrimSpace(c.Param("id"))
	if identityNumber == "" || len(identityNumber) > maxIdentityNumberLength || strings.ContainsRune(identityNumber, 0) {
		apierror.RespondFields(c, apierror.FieldError{Field: "id", Message: "must be a national ID or passport ID"})
		return
	}

	patient, err := handler.patientService.FindByIdentityNumber(c.Request.Context(), staff, identityNumber)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, newPatientResponse(patient))
	case errors.Is(err, domain.ErrPatientNotFound):
		apierror.Respond(c, http.StatusNotFound, apierror.CodeNotFound, "patient not found")
	default:
		handler.logger.ErrorContext(c.Request.Context(), "patient lookup failed", "error", err)
		apierror.Respond(c, http.StatusInternalServerError, apierror.CodeInternal, "could not look up the patient")
	}
}

// bindSearchRequest reads the query string, then the JSON body (if any) on top of it, and validates
// the result. It writes the error response itself and returns ok=false when the request is invalid.
func bindSearchRequest(c *gin.Context) (request searchPatientsRequest, ok bool) {
	// URL.Query() silently drops pairs it cannot decode (e.g. "first_name=%ZZ"), which would widen the search.
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		apierror.Respond(c, http.StatusBadRequest, apierror.CodeValidation, "query string is not correctly URL-encoded")
		return request, false
	}
	for key := range query {
		if !searchFieldNames[key] {
			apierror.RespondFields(c, apierror.FieldError{Field: key, Message: unknownFieldMessage})
			return request, false
		}
	}
	if err := binding.MapFormWithTag(&request, query, "form"); err != nil {
		apierror.RespondValidation(c, err)
		return request, false
	}
	if !decodeJSONBody(c, &request) {
		return request, false
	}
	if err := binding.Validator.ValidateStruct(&request); err != nil {
		apierror.RespondValidation(c, err)
		return request, false
	}
	return request, true
}

// decodeJSONBody strictly decodes an optional JSON body: unknown fields, wrong types, trailing
// data and oversized bodies are rejected; an empty body is fine.
func decodeJSONBody(c *gin.Context, request *searchPatientsRequest) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	err := decoder.Decode(request)
	if errors.Is(err, io.EOF) {
		return true // no body
	}
	if err == nil {
		// Exactly one JSON value is allowed: `{}{"national_id":"..."}` must not silently drop the second object.
		trailingErr := decoder.Decode(&struct{}{})
		switch {
		case errors.Is(trailingErr, io.EOF):
			return true
		case apierror.IsBodyTooLarge(trailingErr):
			apierror.RespondBodyTooLarge(c)
		default:
			apierror.Respond(c, http.StatusBadRequest, apierror.CodeValidation, "request body must contain a single JSON object")
		}
		return false
	}

	var typeErr *json.UnmarshalTypeError
	switch {
	case apierror.IsBodyTooLarge(err):
		apierror.RespondBodyTooLarge(c)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		unknownField := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		apierror.RespondFields(c, apierror.FieldError{Field: unknownField, Message: unknownFieldMessage})
	case errors.As(err, &typeErr) && typeErr.Field != "":
		apierror.RespondFields(c, apierror.FieldError{Field: typeErr.Field, Message: describeJSONType(typeErr.Type)})
	default:
		apierror.Respond(c, http.StatusBadRequest, apierror.CodeValidation, "request body is not valid JSON")
	}
	return false
}

// describeJSONType tells the client which JSON type a field expects.
func describeJSONType(expectedType reflect.Type) string {
	for expectedType.Kind() == reflect.Pointer {
		expectedType = expectedType.Elem()
	}
	if expectedType.Kind() == reflect.String {
		return "must be a JSON string"
	}
	return "must be a JSON number"
}

// toCriteria normalises the request and runs the checks that struct tags cannot express.
func (request searchPatientsRequest) toCriteria() (domain.PatientSearchCriteria, []apierror.FieldError) {
	var fieldErrors []apierror.FieldError
	textFields := []struct{ name, value string }{
		{"national_id", request.NationalID}, {"passport_id", request.PassportID},
		{"first_name", request.FirstName}, {"middle_name", request.MiddleName}, {"last_name", request.LastName},
		{"phone_number", request.PhoneNumber}, {"email", request.Email},
	}
	for _, field := range textFields {
		// PostgreSQL text cannot hold U+0000; without this check the query fails with a 500.
		if strings.ContainsRune(field.value, 0) {
			fieldErrors = append(fieldErrors, apierror.FieldError{Field: field.name, Message: "must not contain NUL characters"})
		}
	}

	criteria := domain.PatientSearchCriteria{
		NationalID:  domain.NormalizeNationalID(request.NationalID),
		PassportID:  domain.NormalizePassportID(request.PassportID),
		FirstName:   strings.TrimSpace(request.FirstName),
		MiddleName:  strings.TrimSpace(request.MiddleName),
		LastName:    strings.TrimSpace(request.LastName),
		PhoneNumber: strings.TrimSpace(request.PhoneNumber),
		Email:       strings.TrimSpace(request.Email),
	}
	if strings.TrimSpace(request.NationalID) != "" && len(criteria.NationalID) != domain.NationalIDLength {
		fieldErrors = append(fieldErrors, apierror.FieldError{Field: "national_id", Message: "must contain 13 digits"})
	}
	if criteria.PhoneNumber != "" && !strings.ContainsAny(criteria.PhoneNumber, "0123456789") {
		fieldErrors = append(fieldErrors, apierror.FieldError{Field: "phone_number", Message: "must contain digits"})
	}
	if request.DateOfBirth != "" {
		// Already validated by the datetime binding rule.
		dateOfBirth, _ := time.Parse(dateLayout, request.DateOfBirth)
		criteria.DateOfBirth = &dateOfBirth
	}
	if request.Limit != nil {
		criteria.Limit = *request.Limit
	}
	if request.Offset != nil {
		criteria.Offset = *request.Offset
	}
	return criteria, fieldErrors
}

func newSearchPatientsResponse(result domain.PatientSearchResult) searchPatientsResponse {
	data := make([]patientResponse, 0, len(result.Patients))
	for _, patient := range result.Patients {
		data = append(data, newPatientResponse(patient))
	}
	return searchPatientsResponse{
		Data:       data,
		Pagination: paginationResponse{Total: result.Total, Limit: result.Limit, Offset: result.Offset},
	}
}

func newPatientResponse(patient domain.Patient) patientResponse {
	response := patientResponse{
		FirstNameTH:  nullableString(patient.FirstNameTH),
		MiddleNameTH: nullableString(patient.MiddleNameTH),
		LastNameTH:   nullableString(patient.LastNameTH),
		FirstNameEN:  nullableString(patient.FirstNameEN),
		MiddleNameEN: nullableString(patient.MiddleNameEN),
		LastNameEN:   nullableString(patient.LastNameEN),
		PatientHN:    patient.PatientHN,
		NationalID:   nullableString(patient.NationalID),
		PassportID:   nullableString(patient.PassportID),
		PhoneNumber:  nullableString(patient.PhoneNumber),
		Email:        nullableString(patient.Email),
		Gender:       nullableString(string(patient.Gender)),
	}
	if patient.DateOfBirth != nil {
		formattedDate := patient.DateOfBirth.Format(dateLayout)
		response.DateOfBirth = &formattedDate
	}
	return response
}

// nullableString maps "not provided" ("") to JSON null.
func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
