package router_test

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/jakapobrs-oss/hospital-middleware/internal/auth"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/handler"
	"github.com/jakapobrs-oss/hospital-middleware/internal/router"
	"github.com/jakapobrs-oss/hospital-middleware/internal/service"
)

// These tests wire the real router, auth middleware, token manager and handlers together,
// with fake services underneath, to check routing and authentication end to end.

const testSecret = "router-test-secret-0123456789abcdef"

type stubDatabase struct{}

func (stubDatabase) Ping(context.Context) error { return nil }

type stubStaffService struct{}

func (stubStaffService) Create(context.Context, service.CreateStaffInput) (service.CreatedStaff, error) {
	return service.CreatedStaff{
		Staff:    domain.Staff{ID: 1, Username: "nurse.a", CreatedAt: time.Now()},
		Hospital: domain.Hospital{ID: 10, Code: "hospital-a", Name: "Hospital A"},
	}, nil
}

func (stubStaffService) Login(context.Context, service.LoginInput) (service.LoginResult, error) {
	return service.LoginResult{}, domain.ErrInvalidCredentials
}

// spyPatientService remembers which staff member the search was made for.
type spyPatientService struct {
	searchedBy *domain.StaffIdentity
}

func (spy *spyPatientService) Search(_ context.Context, staff domain.StaffIdentity, _ domain.PatientSearchCriteria) (domain.PatientSearchResult, error) {
	spy.searchedBy = &staff
	return domain.PatientSearchResult{Limit: 20}, nil
}

func (spy *spyPatientService) FindByIdentityNumber(_ context.Context, staff domain.StaffIdentity, _ string) (domain.Patient, error) {
	spy.searchedBy = &staff
	return domain.Patient{PatientHN: "HN-B-000001"}, nil
}

func newTestEngine(t *testing.T) (*gin.Engine, *spyPatientService, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenManager := auth.NewTokenManager(testSecret, time.Hour)
	patientService := &spyPatientService{}

	engine := router.New(router.Handlers{
		Health:  handler.NewHealthHandler(stubDatabase{}),
		Staff:   handler.NewStaffHandler(stubStaffService{}),
		Patient: handler.NewPatientHandler(patientService, logger),
	}, tokenManager, logger)
	return engine, patientService, tokenManager
}

func send(engine *gin.Engine, method, target, body, bearerToken string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	return body.Error.Code
}

func TestRouter_PublicRoutes(t *testing.T) {
	engine, _, _ := newTestEngine(t)

	assert.Equal(t, http.StatusOK, send(engine, http.MethodGet, "/health", "", "").Code)
	assert.Equal(t, http.StatusCreated, send(engine, http.MethodPost, "/staff/create",
		`{"username":"nurse.a","password":"S3cure-Passw0rd","hospital":"hospital-a"}`, "").Code)
	assert.Equal(t, http.StatusUnauthorized, send(engine, http.MethodPost, "/staff/login",
		`{"username":"nurse.a","password":"wrong-password","hospital":"hospital-a"}`, "").Code)
}

func TestRouter_PatientSearchRequiresValidToken(t *testing.T) {
	engine, patientService, _ := newTestEngine(t)
	otherSystem := auth.NewTokenManager("a-different-secret-0123456789abcdef", time.Hour)
	forgedToken, _, err := otherSystem.Issue(domain.StaffIdentity{StaffID: 1, HospitalID: 10, HospitalCode: "hospital-a", Username: "x"})
	require.NoError(t, err)

	testCases := map[string]string{
		"no token":     "",
		"garbage":      "not-a-jwt",
		"forged token": forgedToken,
	}
	for name, token := range testCases {
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				recorder := send(engine, method, "/patient/search", `{}`, token)

				assert.Equal(t, http.StatusUnauthorized, recorder.Code, method)
				assert.Equal(t, "unauthorized", errorCode(t, recorder))
			}
			assert.Nil(t, patientService.searchedBy, "the service must not be reached")
		})
	}
}

func TestRouter_PatientSearchUsesHospitalFromToken(t *testing.T) {
	engine, patientService, tokenManager := newTestEngine(t)
	hospitalBStaff := domain.StaffIdentity{StaffID: 5, HospitalID: 20, HospitalCode: "hospital-b", Username: "nurse.b"}
	token, _, err := tokenManager.Issue(hospitalBStaff)
	require.NoError(t, err)

	// A client cannot pick the hospital: such a field is rejected, never silently applied or ignored.
	rejected := send(engine, http.MethodPost, "/patient/search", `{"hospital":"hospital-a","hospital_id":10}`, token)
	assert.Equal(t, http.StatusBadRequest, rejected.Code)
	assert.Nil(t, patientService.searchedBy)

	recorder := send(engine, http.MethodPost, "/patient/search", `{}`, token)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, patientService.searchedBy)
	assert.Equal(t, hospitalBStaff, *patientService.searchedBy, "the token decides the hospital")
}

func TestRouter_PatientLookupByIdentityNumber(t *testing.T) {
	engine, patientService, tokenManager := newTestEngine(t)
	token, _, err := tokenManager.Issue(domain.StaffIdentity{StaffID: 5, HospitalID: 20, HospitalCode: "hospital-b", Username: "nurse.b"})
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnauthorized, send(engine, http.MethodGet, "/patient/search/1100000000016", "", "").Code)

	recorder := send(engine, http.MethodGet, "/patient/search/1100000000016", "", token)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, patientService.searchedBy)
	assert.Equal(t, int64(20), patientService.searchedBy.HospitalID)
}

func TestRouter_RejectsOversizedBodies(t *testing.T) {
	engine, patientService, tokenManager := newTestEngine(t)
	token, _, err := tokenManager.Issue(domain.StaffIdentity{StaffID: 1, HospitalID: 10, HospitalCode: "hospital-a", Username: "nurse.a"})
	require.NoError(t, err)
	oversizedBody := `{"first_name":"` + strings.Repeat("a", 2<<20) + `"}`

	recorder := send(engine, http.MethodPost, "/patient/search", oversizedBody, token)

	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Equal(t, "payload_too_large", errorCode(t, recorder))
	assert.Nil(t, patientService.searchedBy)
}

func TestRouter_PanicBecomesJSON500(t *testing.T) {
	engine, _, _ := newTestEngine(t)
	engine.GET("/panic", func(*gin.Context) { panic("boom") })

	recorder := send(engine, http.MethodGet, "/panic?national_id=1100000000016", "", "")

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, "internal_error", errorCode(t, recorder))
}

func TestRouter_LogsNeverContainIdentityNumbers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	tokenManager := auth.NewTokenManager(testSecret, time.Hour)
	engine := router.New(router.Handlers{
		Health:  handler.NewHealthHandler(stubDatabase{}),
		Staff:   handler.NewStaffHandler(stubStaffService{}),
		Patient: handler.NewPatientHandler(&spyPatientService{}, logger),
	}, tokenManager, logger)
	engine.GET("/panic/:id", func(*gin.Context) { panic("boom") })
	token, _, err := tokenManager.Issue(domain.StaffIdentity{StaffID: 1, HospitalID: 10, HospitalCode: "hospital-a", Username: "nurse.a"})
	require.NoError(t, err)

	send(engine, http.MethodGet, "/patient/search/1100000000016", "", token)
	send(engine, http.MethodGet, "/patient/search?national_id=1100000000024", "", token)
	send(engine, http.MethodGet, "/panic/1100000000032", "", "")
	send(engine, http.MethodGet, "/no-such-route/1100000000041", "", "")

	logs := logOutput.String()
	for _, identityNumber := range []string{"1100000000016", "1100000000024", "1100000000032", "1100000000041"} {
		assert.NotContains(t, logs, identityNumber)
	}
	assert.Contains(t, logs, `"route":"/patient/search/:id"`)
	assert.Contains(t, logs, `"route":"unmatched"`)
	assert.Contains(t, logs, "panic while handling request")
}

func TestRouter_UnknownRouteReturnsJSON404(t *testing.T) {
	engine, _, _ := newTestEngine(t)

	recorder := send(engine, http.MethodGet, "/does-not-exist", "", "")

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "not_found", errorCode(t, recorder))
}
