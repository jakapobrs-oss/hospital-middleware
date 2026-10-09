package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/service"
)

const (
	staffTestPassword  = "S3cure-Passw0rd"
	staffTestUsername  = "nurse.somchai"
	staffTestHospitalA = "hospital-a"
)

var (
	staffTestHospital = domain.Hospital{ID: 1, Code: staffTestHospitalA, Name: "Hospital A"}
	staffTestNow      = time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)
)

func init() {
	gin.SetMode(gin.TestMode)
	// Failure paths log on purpose; keep the test output readable.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// fakeStaffService is a hand-written StaffService; each test sets the func it needs and reads the recorded inputs.
type fakeStaffService struct {
	create       func(ctx context.Context, input service.CreateStaffInput) (service.CreatedStaff, error)
	login        func(ctx context.Context, input service.LoginInput) (service.LoginResult, error)
	createInputs []service.CreateStaffInput
	loginInputs  []service.LoginInput
}

var _ StaffService = (*fakeStaffService)(nil)

func (f *fakeStaffService) Create(ctx context.Context, input service.CreateStaffInput) (service.CreatedStaff, error) {
	f.createInputs = append(f.createInputs, input)
	return f.create(ctx, input)
}

func (f *fakeStaffService) Login(ctx context.Context, input service.LoginInput) (service.LoginResult, error) {
	f.loginInputs = append(f.loginInputs, input)
	return f.login(ctx, input)
}

// createSucceeds is a fake Create that answers every request with a new account of hospital A.
func createSucceeds(context.Context, service.CreateStaffInput) (service.CreatedStaff, error) {
	return service.CreatedStaff{
		Staff:    domain.Staff{ID: 1, HospitalID: 1, Username: staffTestUsername, CreatedAt: staffTestNow},
		Hospital: staffTestHospital,
	}, nil
}

// loginSucceeds is a fake Login that answers every request with a one-hour token for a nurse of hospital A.
func loginSucceeds(context.Context, service.LoginInput) (service.LoginResult, error) {
	return service.LoginResult{
		AccessToken: "jwt-token",
		ExpiresAt:   staffTestNow.Add(time.Hour),
		Staff:       domain.Staff{ID: 1, HospitalID: 1, Username: staffTestUsername},
		Hospital:    staffTestHospital,
	}, nil
}

// newStaffTestRouter serves the staff routes through a handler that uses fake and a frozen clock.
func newStaffTestRouter(fake *fakeStaffService) *gin.Engine {
	staffHandler := NewStaffHandler(fake)
	staffHandler.now = func() time.Time { return staffTestNow }

	router := gin.New()
	router.POST("/staff/create", staffHandler.Create)
	router.POST("/staff/login", staffHandler.Login)
	return router
}

func postStaffJSON(router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// staffRequestBody builds the JSON body shared by /staff/create and /staff/login.
func staffRequestBody(t *testing.T, username, password, hospital string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password, "hospital": hospital})
	require.NoError(t, err)
	return string(body)
}

func decodeStaffError(t *testing.T, recorder *httptest.ResponseRecorder) apierror.Detail {
	t.Helper()
	var body apierror.Body
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), "error responses must use the JSON envelope")
	return body.Error
}

func fieldNames(fields []apierror.FieldError) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Field)
	}
	return names
}

func TestStaffHandler_Create_succeeds(t *testing.T) {
	fake := &fakeStaffService{create: func(context.Context, service.CreateStaffInput) (service.CreatedStaff, error) {
		return service.CreatedStaff{
			Staff: domain.Staff{
				ID:           1,
				HospitalID:   1,
				Username:     staffTestUsername,
				PasswordHash: "hash-that-must-not-be-returned",
				// 10:15 in Bangkok is 03:15 UTC; the API always answers in UTC.
				CreatedAt: time.Date(2026, 10, 10, 10, 15, 0, 0, time.FixedZone("ICT", 7*60*60)),
			},
			Hospital: staffTestHospital,
		}, nil
	}}

	recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/create",
		staffRequestBody(t, staffTestUsername, staffTestPassword, staffTestHospitalA))

	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.JSONEq(t,
		`{"id":1,"username":"nurse.somchai","hospital":{"code":"hospital-a","name":"Hospital A"},"created_at":"2026-10-10T03:15:00Z"}`,
		recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "hash-that-must-not-be-returned")
	assert.Equal(t,
		[]service.CreateStaffInput{{Username: staffTestUsername, Password: staffTestPassword, HospitalCode: staffTestHospitalA}},
		fake.createInputs)
}

func TestStaffHandler_Create_acceptsValuesAtTheLimits(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		hospital string
	}{
		{name: "shortest username", username: "abc", password: staffTestPassword, hospital: staffTestHospitalA},
		{name: "longest username", username: strings.Repeat("a", 50), password: staffTestPassword, hospital: staffTestHospitalA},
		{name: "every allowed username character", username: "Nurse.One_2-x", password: staffTestPassword, hospital: staffTestHospitalA},
		{name: "e-mail address as username", username: "nurse.somchai@hospital-a.example", password: staffTestPassword, hospital: staffTestHospitalA},
		{name: "plus tag in the username", username: "nurse+night@example.com", password: staffTestPassword, hospital: staffTestHospitalA},
		{name: "shortest password", username: staffTestUsername, password: "12345678", hospital: staffTestHospitalA},
		{name: "longest password", username: staffTestUsername, password: strings.Repeat("a", 72), hospital: staffTestHospitalA},
		{name: "password with spaces and symbols", username: staffTestUsername, password: "  pass word! ", hospital: staffTestHospitalA},
		{name: "thai password within 72 bytes", username: staffTestUsername, password: strings.Repeat("ก", 24), hospital: staffTestHospitalA},
		{name: "hospital given by name", username: staffTestUsername, password: staffTestPassword, hospital: "Hospital A"},
		{name: "longest hospital reference", username: staffTestUsername, password: staffTestPassword, hospital: strings.Repeat("h", 100)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStaffService{create: createSucceeds}

			recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/create",
				staffRequestBody(t, tc.username, tc.password, tc.hospital))

			assert.Equal(t, http.StatusCreated, recorder.Code)
			assert.Len(t, fake.createInputs, 1)
		})
	}
}

func TestStaffHandler_Create_rejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantFields []string // fields named in the error response; empty for a malformed body
	}{
		{name: "empty object", body: `{}`, wantFields: []string{"username", "password", "hospital"}},
		{name: "username too short", body: staffRequestBody(t, "ab", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username too long", body: staffRequestBody(t, strings.Repeat("a", 51), staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username with a space", body: staffRequestBody(t, "nurse one", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username with a hash sign", body: staffRequestBody(t, "nurse#1", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username with a slash", body: staffRequestBody(t, "nurse/one", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username with a comma", body: staffRequestBody(t, "nurse,one", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username with a trailing newline", body: staffRequestBody(t, "nurse01\n", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "username in thai letters", body: staffRequestBody(t, "นางพยาบาล", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "password too short", body: staffRequestBody(t, staffTestUsername, "1234567", staffTestHospitalA), wantFields: []string{"password"}},
		{name: "password longer than 72 characters", body: staffRequestBody(t, staffTestUsername, strings.Repeat("a", 73), staffTestHospitalA), wantFields: []string{"password"}},
		{name: "password within 72 characters but over 72 bytes", body: staffRequestBody(t, staffTestUsername, strings.Repeat("ก", 25), staffTestHospitalA), wantFields: []string{"password"}},
		{name: "hospital missing", body: staffRequestBody(t, staffTestUsername, staffTestPassword, ""), wantFields: []string{"hospital"}},
		{name: "hospital reference too long", body: staffRequestBody(t, staffTestUsername, staffTestPassword, strings.Repeat("h", 101)), wantFields: []string{"hospital"}},
		{name: "several problems at once", body: staffRequestBody(t, "no", "short", ""), wantFields: []string{"username", "password", "hospital"}},
		{name: "empty body", body: ``},
		{name: "broken json", body: `{"username":`},
		{name: "json array instead of object", body: `[]`},
		{name: "field of the wrong type", body: `{"username":123,"password":"S3cure-Passw0rd","hospital":"hospital-a"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStaffService{create: createSucceeds}

			recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/create", tc.body)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			errorDetail := decodeStaffError(t, recorder)
			assert.Equal(t, apierror.CodeValidation, errorDetail.Code)
			assert.ElementsMatch(t, tc.wantFields, fieldNames(errorDetail.Fields))
			assert.Empty(t, fake.createInputs, "an invalid request must not reach the service")
		})
	}

	t.Run("explains the byte limit of the password", func(t *testing.T) {
		fake := &fakeStaffService{create: createSucceeds}

		recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/create",
			staffRequestBody(t, staffTestUsername, strings.Repeat("ก", 25), staffTestHospitalA))

		errorDetail := decodeStaffError(t, recorder)
		require.Len(t, errorDetail.Fields, 1)
		assert.Equal(t, apierror.FieldError{Field: "password", Message: "must be at most 72 bytes"}, errorDetail.Fields[0])
	})
}

func TestStaffHandler_Create_mapsServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "unknown hospital",
			serviceErr:  domain.ErrHospitalNotFound,
			wantStatus:  http.StatusBadRequest,
			wantCode:    apierror.CodeUnknownHospital,
			wantMessage: "hospital not found: use its code (for example hospital-a) or its name",
		},
		{
			name:        "registration key missing or wrong",
			serviceErr:  domain.ErrInvalidRegistrationKey,
			wantStatus:  http.StatusForbidden,
			wantCode:    apierror.CodeInvalidRegistrationKey,
			wantMessage: "staff registration needs a valid X-Registration-Key header",
		},
		{
			name:        "wrapped registration key error",
			serviceErr:  fmt.Errorf("staff service: %w", domain.ErrInvalidRegistrationKey),
			wantStatus:  http.StatusForbidden,
			wantCode:    apierror.CodeInvalidRegistrationKey,
			wantMessage: "staff registration needs a valid X-Registration-Key header",
		},
		{
			name:        "username already taken",
			serviceErr:  domain.ErrUsernameTaken,
			wantStatus:  http.StatusConflict,
			wantCode:    apierror.CodeUsernameTaken,
			wantMessage: "username already exists in this hospital",
		},
		{
			name:        "wrapped domain error",
			serviceErr:  fmt.Errorf("staff service: create: save staff: %w", domain.ErrUsernameTaken),
			wantStatus:  http.StatusConflict,
			wantCode:    apierror.CodeUsernameTaken,
			wantMessage: "username already exists in this hospital",
		},
		{
			name:        "unexpected error",
			serviceErr:  errors.New(`password authentication failed for user "hospital"`),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    apierror.CodeInternal,
			wantMessage: "internal server error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStaffService{create: func(context.Context, service.CreateStaffInput) (service.CreatedStaff, error) {
				return service.CreatedStaff{}, tc.serviceErr
			}}

			recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/create",
				staffRequestBody(t, staffTestUsername, staffTestPassword, staffTestHospitalA))

			assert.Equal(t, tc.wantStatus, recorder.Code)
			errorDetail := decodeStaffError(t, recorder)
			assert.Equal(t, tc.wantCode, errorDetail.Code)
			assert.Equal(t, tc.wantMessage, errorDetail.Message)
			assert.NotContains(t, recorder.Body.String(), "authentication failed", "internal details must not leak")
		})
	}
}

func TestStaffHandler_Create_passesTheRegistrationKeyHeaderToTheService(t *testing.T) {
	const registrationKey = "let-me-in-2026"
	body := staffRequestBody(t, staffTestUsername, staffTestPassword, staffTestHospitalA)

	sendWithHeader := func(fake *fakeStaffService, headerName, headerValue string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/staff/create", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if headerName != "" {
			request.Header.Set(headerName, headerValue)
		}
		recorder := httptest.NewRecorder()
		newStaffTestRouter(fake).ServeHTTP(recorder, request)
		return recorder
	}

	tests := []struct {
		name        string
		headerName  string
		headerValue string
		wantKey     string
	}{
		{name: "header sent", headerName: "X-Registration-Key", headerValue: registrationKey, wantKey: registrationKey},
		{name: "header name in another letter case", headerName: "x-registration-key", headerValue: registrationKey, wantKey: registrationKey},
		{name: "no header", wantKey: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStaffService{create: createSucceeds}

			recorder := sendWithHeader(fake, tc.headerName, tc.headerValue)

			require.Equal(t, http.StatusCreated, recorder.Code)
			require.Len(t, fake.createInputs, 1)
			assert.Equal(t, tc.wantKey, fake.createInputs[0].RegistrationKey)
		})
	}

	t.Run("a rejected key is never echoed back", func(t *testing.T) {
		fake := &fakeStaffService{create: func(context.Context, service.CreateStaffInput) (service.CreatedStaff, error) {
			return service.CreatedStaff{}, domain.ErrInvalidRegistrationKey
		}}

		recorder := sendWithHeader(fake, "X-Registration-Key", registrationKey)

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.NotContains(t, recorder.Body.String(), registrationKey)
	})

	t.Run("login ignores the header", func(t *testing.T) {
		fake := &fakeStaffService{login: loginSucceeds}
		request := httptest.NewRequest(http.MethodPost, "/staff/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Registration-Key", registrationKey)
		recorder := httptest.NewRecorder()

		newStaffTestRouter(fake).ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusOK, recorder.Code)
	})
}

func TestStaffHandler_Login_succeeds(t *testing.T) {
	fake := &fakeStaffService{login: func(context.Context, service.LoginInput) (service.LoginResult, error) {
		return service.LoginResult{
			AccessToken: "jwt-token",
			ExpiresAt:   staffTestNow.Add(time.Hour),
			Staff:       domain.Staff{ID: 1, HospitalID: 1, Username: staffTestUsername, PasswordHash: "hash-that-must-not-be-returned"},
			Hospital:    staffTestHospital,
		}, nil
	}}

	recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/login",
		staffRequestBody(t, staffTestUsername, staffTestPassword, staffTestHospitalA))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t,
		`{"access_token":"jwt-token","token_type":"Bearer","expires_in":3600,"staff":{"id":1,"username":"nurse.somchai","hospital":{"code":"hospital-a","name":"Hospital A"}}}`,
		recorder.Body.String())
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"), "a response with a token must not be cached")
	assert.NotContains(t, recorder.Body.String(), "hash-that-must-not-be-returned")
	assert.Equal(t,
		[]service.LoginInput{{Username: staffTestUsername, Password: staffTestPassword, HospitalCode: staffTestHospitalA}},
		fake.loginInputs)
}

func TestStaffHandler_Login_rejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantFields []string
	}{
		{name: "empty object", body: `{}`, wantFields: []string{"username", "password", "hospital"}},
		{name: "username missing", body: staffRequestBody(t, "", staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "password missing", body: staffRequestBody(t, staffTestUsername, "", staffTestHospitalA), wantFields: []string{"password"}},
		{name: "hospital missing", body: staffRequestBody(t, staffTestUsername, staffTestPassword, ""), wantFields: []string{"hospital"}},
		{name: "username too long", body: staffRequestBody(t, strings.Repeat("a", 51), staffTestPassword, staffTestHospitalA), wantFields: []string{"username"}},
		{name: "password too long", body: staffRequestBody(t, staffTestUsername, strings.Repeat("a", 129), staffTestHospitalA), wantFields: []string{"password"}},
		{name: "hospital reference too long", body: staffRequestBody(t, staffTestUsername, staffTestPassword, strings.Repeat("h", 101)), wantFields: []string{"hospital"}},
		{name: "empty body", body: ``},
		{name: "broken json", body: `{"username":`},
		{name: "field of the wrong type", body: `{"username":"nurse","password":12345678,"hospital":"hospital-a"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStaffService{login: loginSucceeds}

			recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/login", tc.body)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			errorDetail := decodeStaffError(t, recorder)
			assert.Equal(t, apierror.CodeValidation, errorDetail.Code)
			assert.ElementsMatch(t, tc.wantFields, fieldNames(errorDetail.Fields))
			assert.Empty(t, fake.loginInputs, "an invalid request must not reach the service")
		})
	}
}

// A login attempt with a password that could never be valid must look like any other failed login,
// otherwise the endpoint would reveal the password policy.
func TestStaffHandler_Login_doesNotRevealThePasswordPolicy(t *testing.T) {
	fake := &fakeStaffService{login: func(context.Context, service.LoginInput) (service.LoginResult, error) {
		return service.LoginResult{}, domain.ErrInvalidCredentials
	}}

	recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/login",
		staffRequestBody(t, staffTestUsername, "x", staffTestHospitalA))

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Equal(t, apierror.CodeInvalidCredentials, decodeStaffError(t, recorder).Code)
	assert.Len(t, fake.loginInputs, 1, "even a one-character password is passed on to the service")
}

func TestStaffHandler_Login_mapsServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid credentials",
			serviceErr:  domain.ErrInvalidCredentials,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    apierror.CodeInvalidCredentials,
			wantMessage: "invalid username, password or hospital",
		},
		{
			name:        "wrapped invalid credentials",
			serviceErr:  fmt.Errorf("staff service: login: %w", domain.ErrInvalidCredentials),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    apierror.CodeInvalidCredentials,
			wantMessage: "invalid username, password or hospital",
		},
		{
			name:        "unexpected error",
			serviceErr:  errors.New("connection to server at db:5432 refused"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    apierror.CodeInternal,
			wantMessage: "internal server error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStaffService{login: func(context.Context, service.LoginInput) (service.LoginResult, error) {
				return service.LoginResult{}, tc.serviceErr
			}}

			recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/login",
				staffRequestBody(t, staffTestUsername, staffTestPassword, staffTestHospitalA))

			assert.Equal(t, tc.wantStatus, recorder.Code)
			errorDetail := decodeStaffError(t, recorder)
			assert.Equal(t, tc.wantCode, errorDetail.Code)
			assert.Equal(t, tc.wantMessage, errorDetail.Message)
			assert.NotContains(t, recorder.Body.String(), "db:5432", "internal details must not leak")
			assert.Empty(t, recorder.Header().Get("Cache-Control"), "only a successful login carries no-store")
		})
	}
}

func TestStaffHandler_secondsUntil(t *testing.T) {
	staffHandler := NewStaffHandler(&fakeStaffService{})
	staffHandler.now = func() time.Time { return staffTestNow }

	tests := []struct {
		name      string
		expiresAt time.Time
		want      int64
	}{
		{name: "exactly one hour", expiresAt: staffTestNow.Add(time.Hour), want: 3600},
		{name: "a few milliseconds spent since signing", expiresAt: staffTestNow.Add(time.Hour - 40*time.Millisecond), want: 3600},
		{name: "ninety minutes", expiresAt: staffTestNow.Add(90 * time.Minute), want: 5400},
		{name: "expires right now", expiresAt: staffTestNow, want: 0},
		{name: "already expired", expiresAt: staffTestNow.Add(-time.Minute), want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, staffHandler.secondsUntil(tc.expiresAt))
		})
	}
}

func TestStaffHandler_logsServerFailuresWithoutRequestData(t *testing.T) {
	var logOutput bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logOutput, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	fake := &fakeStaffService{create: func(context.Context, service.CreateStaffInput) (service.CreatedStaff, error) {
		return service.CreatedStaff{}, errors.New("insert failed")
	}}

	recorder := postStaffJSON(newStaffTestRouter(fake), "/staff/create",
		staffRequestBody(t, staffTestUsername, staffTestPassword, staffTestHospitalA))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Contains(t, logOutput.String(), "insert failed", "the real cause must be logged")
	assert.NotContains(t, logOutput.String(), staffTestPassword, "passwords must never reach the logs")
}

// PostgreSQL text cannot hold U+0000, so such input must be a 400, never a database error (500).
func TestStaffHandler_RejectsNULCharacters(t *testing.T) {
	testCases := []struct {
		name          string
		path          string
		body          string
		expectedField string
	}{
		{name: "create: hospital", path: "/staff/create",
			body: `{"username":"nurse01","password":"S3cure-Passw0rd","hospital":"hospital-a\u0000"}`, expectedField: "hospital"},
		{name: "login: username", path: "/staff/login",
			body: `{"username":"nurse\u0000","password":"S3cure-Passw0rd","hospital":"hospital-a"}`, expectedField: "username"},
		{name: "login: hospital", path: "/staff/login",
			body: `{"username":"nurse01","password":"S3cure-Passw0rd","hospital":"\u0000"}`, expectedField: "hospital"},
		{name: "create: password", path: "/staff/create",
			body: `{"username":"nurse01","password":"1234567\u0000","hospital":"hospital-a"}`, expectedField: "password"},
		{name: "login: password", path: "/staff/login",
			body: `{"username":"nurse01","password":"S3cure\u0000","hospital":"hospital-a"}`, expectedField: "password"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &fakeStaffService{}
			recorder := postStaffJSON(newStaffTestRouter(fake), testCase.path, testCase.body)

			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			errorDetail := decodeStaffError(t, recorder)
			assert.Equal(t, apierror.CodeValidation, errorDetail.Code)
			require.NotEmpty(t, errorDetail.Fields)
			assert.Equal(t, testCase.expectedField, errorDetail.Fields[0].Field)
			assert.Empty(t, fake.createInputs)
			assert.Empty(t, fake.loginInputs)
		})
	}
}
