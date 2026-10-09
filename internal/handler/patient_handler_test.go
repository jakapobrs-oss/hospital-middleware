package handler_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/handler"
	"github.com/jakapobrs-oss/hospital-middleware/internal/middleware"
)

var nurseOfHospitalA = domain.StaffIdentity{StaffID: 7, HospitalID: 10, HospitalCode: "hospital-a", Username: "nurse.a"}

// fakePatientService records the last call and returns a canned result.
type fakePatientService struct {
	result  domain.PatientSearchResult
	patient domain.Patient
	err     error

	called                 bool
	receivedStaff          domain.StaffIdentity
	receivedQuery          domain.PatientSearchCriteria
	receivedIdentityNumber string
}

func (service *fakePatientService) Search(_ context.Context, staff domain.StaffIdentity, criteria domain.PatientSearchCriteria) (domain.PatientSearchResult, error) {
	service.called = true
	service.receivedStaff = staff
	service.receivedQuery = criteria
	return service.result, service.err
}

func (service *fakePatientService) FindByIdentityNumber(_ context.Context, staff domain.StaffIdentity, identityNumber string) (domain.Patient, error) {
	service.called = true
	service.receivedStaff = staff
	service.receivedIdentityNumber = identityNumber
	return service.patient, service.err
}

// newPatientRouter mounts the handler behind a stand-in for RequireAuth that authenticates as staff (when given).
func newPatientRouter(patientService handler.PatientService, staff *domain.StaffIdentity) *gin.Engine {
	engine := gin.New()
	authenticate := func(c *gin.Context) {
		if staff != nil {
			middleware.SetStaffIdentity(c, *staff)
		}
		c.Next()
	}
	patientHandler := handler.NewPatientHandler(patientService, slog.New(slog.NewTextHandler(io.Discard, nil)))
	engine.GET("/patient/search", authenticate, patientHandler.Search)
	engine.POST("/patient/search", authenticate, patientHandler.Search)
	engine.GET("/patient/search/:id", authenticate, patientHandler.FindByIdentityNumber)
	return engine
}

func TestPatientHandler_Search_ReturnsPatientsInHISFieldFormat(t *testing.T) {
	dateOfBirth := time.Date(1985, time.April, 12, 0, 0, 0, 0, time.UTC)
	patientService := &fakePatientService{result: domain.PatientSearchResult{
		Patients: []domain.Patient{{
			PatientHN: "HN-A-000001", NationalID: "1100000000016",
			FirstNameTH: "สมชาย", LastNameTH: "ใจดี", FirstNameEN: "Somchai", LastNameEN: "Jaidee",
			DateOfBirth: &dateOfBirth, PhoneNumber: "080-000-0001", Email: "somchai.j@example.com",
			Gender: domain.GenderMale,
		}},
		Total: 1, Limit: 20, Offset: 0,
	}}
	engine := newPatientRouter(patientService, &nurseOfHospitalA)

	recorder := performRequest(engine, http.MethodPost, "/patient/search", `{"first_name":"som"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{
		"data": [{
			"first_name_th": "สมชาย", "middle_name_th": null, "last_name_th": "ใจดี",
			"first_name_en": "Somchai", "middle_name_en": null, "last_name_en": "Jaidee",
			"date_of_birth": "1985-04-12", "patient_hn": "HN-A-000001",
			"national_id": "1100000000016", "passport_id": null,
			"phone_number": "080-000-0001", "email": "somchai.j@example.com", "gender": "M"
		}],
		"pagination": {"total": 1, "limit": 20, "offset": 0}
	}`, recorder.Body.String())
}

func TestPatientHandler_Search_PassesAuthenticatedStaffAndNormalisedCriteria(t *testing.T) {
	testCases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{
			name:   "POST with JSON body",
			method: http.MethodPost,
			target: "/patient/search",
			body: `{"national_id":"1-1000-00000-01-6","passport_id":" aa123 ","first_name":" Somchai ",
				"middle_name":"M","last_name":"Jaidee","date_of_birth":"1985-04-12",
				"phone_number":"080-000-0001","email":"somchai.j@example.com","limit":5,"offset":10}`,
		},
		{
			name:   "GET with query string",
			method: http.MethodGet,
			target: "/patient/search?national_id=1-1000-00000-01-6&passport_id=+aa123+&first_name=+Somchai+" +
				"&middle_name=M&last_name=Jaidee&date_of_birth=1985-04-12" +
				"&phone_number=080-000-0001&email=somchai.j%40example.com&limit=5&offset=10",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patientService := &fakePatientService{}
			engine := newPatientRouter(patientService, &nurseOfHospitalA)

			recorder := performRequest(engine, testCase.method, testCase.target, testCase.body)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.Equal(t, nurseOfHospitalA, patientService.receivedStaff)
			criteria := patientService.receivedQuery
			assert.Equal(t, "1100000000016", criteria.NationalID)
			assert.Equal(t, "AA123", criteria.PassportID)
			assert.Equal(t, "Somchai", criteria.FirstName)
			assert.Equal(t, "M", criteria.MiddleName)
			assert.Equal(t, "Jaidee", criteria.LastName)
			require.NotNil(t, criteria.DateOfBirth)
			assert.Equal(t, "1985-04-12", criteria.DateOfBirth.Format("2006-01-02"))
			assert.Equal(t, "080-000-0001", criteria.PhoneNumber)
			assert.Equal(t, "somchai.j@example.com", criteria.Email)
			assert.Equal(t, 5, criteria.Limit)
			assert.Equal(t, 10, criteria.Offset)
		})
	}
}

func TestPatientHandler_Search_WithoutFiltersReturnsEmptyListNotNull(t *testing.T) {
	testCases := map[string]struct {
		method string
		body   string
	}{
		"GET without query":    {method: http.MethodGet},
		"POST with empty body": {method: http.MethodPost},
		"POST with {}":         {method: http.MethodPost, body: `{}`},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			patientService := &fakePatientService{result: domain.PatientSearchResult{Limit: 20}}
			engine := newPatientRouter(patientService, &nurseOfHospitalA)

			recorder := performRequest(engine, testCase.method, "/patient/search", testCase.body)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.True(t, patientService.called)
			assert.Equal(t, domain.PatientSearchCriteria{}, patientService.receivedQuery)
			assert.JSONEq(t, `{"data": [], "pagination": {"total": 0, "limit": 20, "offset": 0}}`, recorder.Body.String())
		})
	}
}

func TestPatientHandler_Search_RejectsInvalidInput(t *testing.T) {
	testCases := []struct {
		name          string
		method        string
		target        string
		body          string
		expectedField string
	}{
		{name: "date in wrong format", method: http.MethodPost, target: "/patient/search", body: `{"date_of_birth":"12/04/1985"}`, expectedField: "date_of_birth"},
		{name: "limit above maximum", method: http.MethodGet, target: "/patient/search?limit=101", expectedField: "limit"},
		{name: "limit zero", method: http.MethodPost, target: "/patient/search", body: `{"limit":0}`, expectedField: "limit"},
		{name: "negative offset", method: http.MethodGet, target: "/patient/search?offset=-1", expectedField: "offset"},
		{name: "national id with too few digits", method: http.MethodPost, target: "/patient/search", body: `{"national_id":"12345"}`, expectedField: "national_id"},
		{name: "name too long", method: http.MethodPost, target: "/patient/search", body: `{"first_name":"` + string(bytes.Repeat([]byte("a"), 101)) + `"}`, expectedField: "first_name"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patientService := &fakePatientService{}
			engine := newPatientRouter(patientService, &nurseOfHospitalA)

			recorder := performRequest(engine, testCase.method, testCase.target, testCase.body)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			body := decodeErrorBody(t, recorder)
			assert.Equal(t, apierror.CodeValidation, body.Error.Code)
			require.NotEmpty(t, body.Error.Fields)
			assert.Equal(t, testCase.expectedField, body.Error.Fields[0].Field)
			assert.False(t, patientService.called, "invalid requests must not reach the service")
		})
	}
}

func TestPatientHandler_Search_RejectsMalformedRequests(t *testing.T) {
	testCases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "malformed JSON", method: http.MethodPost, target: "/patient/search", body: `{"first_name":`},
		{name: "limit is not a number", method: http.MethodGet, target: "/patient/search?limit=ten"},
		{name: "field has the wrong JSON type", method: http.MethodPost, target: "/patient/search", body: `{"limit":"ten"}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patientService := &fakePatientService{}
			engine := newPatientRouter(patientService, &nurseOfHospitalA)

			recorder := performRequest(engine, testCase.method, testCase.target, testCase.body)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, apierror.CodeValidation, decodeErrorBody(t, recorder).Error.Code)
			assert.False(t, patientService.called)
		})
	}
}

func TestPatientHandler_Search_RequiresAuthenticatedStaff(t *testing.T) {
	patientService := &fakePatientService{}
	engine := newPatientRouter(patientService, nil)

	recorder := performRequest(engine, http.MethodPost, "/patient/search", `{}`)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Equal(t, apierror.CodeUnauthorized, decodeErrorBody(t, recorder).Error.Code)
	assert.False(t, patientService.called)
}

func TestPatientHandler_Search_HidesInternalErrors(t *testing.T) {
	patientService := &fakePatientService{err: errors.New("pq: connection refused at 10.0.0.5")}
	engine := newPatientRouter(patientService, &nurseOfHospitalA)

	recorder := performRequest(engine, http.MethodPost, "/patient/search", `{}`)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	body := decodeErrorBody(t, recorder)
	assert.Equal(t, apierror.CodeInternal, body.Error.Code)
	assert.NotContains(t, body.Error.Message, "10.0.0.5")
}

// A filter that is silently ignored widens the search to the whole hospital, so every
// unrecognised or malformed input must be rejected instead.
func TestPatientHandler_Search_RejectsInputThatWouldBeIgnored(t *testing.T) {
	testCases := []struct {
		name          string
		method        string
		target        string
		body          string
		expectedField string // "" when the error is not about one field
	}{
		{name: "unknown query parameter", method: http.MethodGet, target: "/patient/search?name=som", expectedField: "name"},
		{name: "query that is not URL-encoded correctly", method: http.MethodGet, target: "/patient/search?first_name=%ZZ"},
		{name: "query value that is not valid UTF-8", method: http.MethodGet, target: "/patient/search?first_name=%FF", expectedField: "first_name"},
		{name: "same query parameter twice", method: http.MethodGet, target: "/patient/search?first_name=&first_name=John", expectedField: "first_name"},
		{name: "national ID with a stray letter", method: http.MethodPost, target: "/patient/search", body: `{"national_id":"x1100000000016"}`, expectedField: "national_id"},
		{name: "unknown JSON field", method: http.MethodPost, target: "/patient/search", body: `{"firstName":"som"}`, expectedField: "firstName"},
		{name: "second JSON object after the first", method: http.MethodPost, target: "/patient/search", body: `{}{"national_id":"1100000000032"}`},
		{name: "NUL character", method: http.MethodPost, target: "/patient/search", body: `{"first_name":"\u0000"}`, expectedField: "first_name"},
		{name: "phone number without digits", method: http.MethodPost, target: "/patient/search", body: `{"phone_number":"abc"}`, expectedField: "phone_number"},
		{name: "national ID sent as a JSON number", method: http.MethodPost, target: "/patient/search", body: `{"national_id":1100000000016}`, expectedField: "national_id"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patientService := &fakePatientService{}
			engine := newPatientRouter(patientService, &nurseOfHospitalA)

			recorder := performRequest(engine, testCase.method, testCase.target, testCase.body)

			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			body := decodeErrorBody(t, recorder)
			assert.Equal(t, apierror.CodeValidation, body.Error.Code)
			if testCase.expectedField != "" {
				require.NotEmpty(t, body.Error.Fields)
				assert.Equal(t, testCase.expectedField, body.Error.Fields[0].Field)
			}
			assert.False(t, patientService.called)
		})
	}
}

func TestPatientHandler_Search_CombinesQueryAndBody(t *testing.T) {
	testCases := []struct {
		name              string
		method            string
		target            string
		body              string
		expectedFirstName string
		expectedLastName  string
	}{
		{name: "POST with filters in the query and the body", method: http.MethodPost,
			target: "/patient/search?first_name=som", body: `{"last_name":"jaidee"}`,
			expectedFirstName: "som", expectedLastName: "jaidee"},
		{name: "the body wins when both set the same field", method: http.MethodPost,
			target: "/patient/search?first_name=som", body: `{"first_name":"wichai"}`,
			expectedFirstName: "wichai"},
		{name: "GET with a JSON body", method: http.MethodGet,
			target: "/patient/search", body: `{"first_name":"som"}`,
			expectedFirstName: "som"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patientService := &fakePatientService{}
			engine := newPatientRouter(patientService, &nurseOfHospitalA)

			recorder := performRequest(engine, testCase.method, testCase.target, testCase.body)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.Equal(t, testCase.expectedFirstName, patientService.receivedQuery.FirstName)
			assert.Equal(t, testCase.expectedLastName, patientService.receivedQuery.LastName)
		})
	}
}

func TestPatientHandler_FindByIdentityNumber_ReturnsOnePatient(t *testing.T) {
	patientService := &fakePatientService{patient: domain.Patient{
		PatientHN: "HN-A-000003", PassportID: "AA1234567", FirstNameEN: "John", LastNameEN: "Smith",
	}}
	engine := newPatientRouter(patientService, &nurseOfHospitalA)

	recorder := performRequest(engine, http.MethodGet, "/patient/search/aa1234567", "")

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, "aa1234567", patientService.receivedIdentityNumber)
	assert.Equal(t, nurseOfHospitalA, patientService.receivedStaff)
	assert.JSONEq(t, `{
		"first_name_th": null, "middle_name_th": null, "last_name_th": null,
		"first_name_en": "John", "middle_name_en": null, "last_name_en": "Smith",
		"date_of_birth": null, "patient_hn": "HN-A-000003",
		"national_id": null, "passport_id": "AA1234567",
		"phone_number": null, "email": null, "gender": null
	}`, recorder.Body.String())
}

func TestPatientHandler_FindByIdentityNumber_Errors(t *testing.T) {
	testCases := []struct {
		name           string
		staff          *domain.StaffIdentity
		target         string
		serviceErr     error
		expectedStatus int
		expectedCode   string
	}{
		{name: "not found", staff: &nurseOfHospitalA, target: "/patient/search/1100000000099",
			serviceErr: domain.ErrPatientNotFound, expectedStatus: http.StatusNotFound, expectedCode: apierror.CodeNotFound},
		{name: "identifier too long", staff: &nurseOfHospitalA, target: "/patient/search/" + string(bytes.Repeat([]byte("9"), 21)),
			expectedStatus: http.StatusBadRequest, expectedCode: apierror.CodeValidation},
		{name: "identifier that is not valid UTF-8", staff: &nurseOfHospitalA, target: "/patient/search/%FF",
			expectedStatus: http.StatusBadRequest, expectedCode: apierror.CodeValidation},
		{name: "service failure is hidden", staff: &nurseOfHospitalA, target: "/patient/search/1100000000016",
			serviceErr: errors.New("connection refused"), expectedStatus: http.StatusInternalServerError, expectedCode: apierror.CodeInternal},
		{name: "not authenticated", staff: nil, target: "/patient/search/1100000000016",
			expectedStatus: http.StatusUnauthorized, expectedCode: apierror.CodeUnauthorized},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patientService := &fakePatientService{err: testCase.serviceErr}
			engine := newPatientRouter(patientService, testCase.staff)

			recorder := performRequest(engine, http.MethodGet, testCase.target, "")

			assert.Equal(t, testCase.expectedStatus, recorder.Code)
			assert.Equal(t, testCase.expectedCode, decodeErrorBody(t, recorder).Error.Code)
		})
	}
}
