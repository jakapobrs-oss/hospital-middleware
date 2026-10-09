package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/demodata"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
)

func get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	newMockHISHandler(demodata.HospitalAHISPatients()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestMockHIS_FindsPatientByNationalID(t *testing.T) {
	recorder := get(t, "/patient/search/1100000000032")

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload his.PatientPayload
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.Equal(t, "HN-A-000004", payload.PatientHN)
	assert.Equal(t, "2000-07-21", payload.DateOfBirth)
}

func TestMockHIS_FindsPatientByPassportCaseInsensitively(t *testing.T) {
	recorder := get(t, "/patient/search/aa1234567")

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"patient_hn":"HN-A-000003"`)
}

func TestMockHIS_UnknownPatientIs404(t *testing.T) {
	assert.Equal(t, http.StatusNotFound, get(t, "/patient/search/1999999999999").Code)
}

func TestMockHIS_Health(t *testing.T) {
	assert.Equal(t, http.StatusOK, get(t, "/health").Code)
}
