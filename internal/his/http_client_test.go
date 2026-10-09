package his_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
)

const somchaiNationalID = "1100000000016"

func somchaiPayload() his.PatientPayload {
	return his.PatientPayload{
		FirstNameTH: "สมชาย",
		LastNameTH:  "ใจดี",
		FirstNameEN: "Somchai",
		LastNameEN:  "Jaidee",
		DateOfBirth: "1985-04-12",
		PatientHN:   "HN-A-000001",
		NationalID:  somchaiNationalID,
		PhoneNumber: "080-000-0001",
		Email:       "somchai.j@example.com",
		Gender:      "M",
	}
}

// newFakeHIS starts an HTTP server that answers GET /patient/search/{id} with the given handler.
func newFakeHIS(t *testing.T, handle func(w http.ResponseWriter, identityNumber string)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /patient/search/{id}", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r.PathValue("id"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// writeJSON runs inside the fake server's goroutine, where t.FailNow must not be called,
// so encoding errors are ignored; a broken response surfaces as a decode error in the client under test.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestHTTPClient_FindPatient_ReturnsPatientFromHIS(t *testing.T) {
	var requestedID string
	server := newFakeHIS(t, func(w http.ResponseWriter, identityNumber string) {
		requestedID = identityNumber
		writeJSON(t, w, http.StatusOK, somchaiPayload())
	})
	client := his.NewHTTPClient(server.URL+"/", time.Second)

	patient, err := client.FindPatient(context.Background(), somchaiNationalID)

	require.NoError(t, err)
	assert.Equal(t, somchaiNationalID, requestedID)
	assert.Equal(t, "HN-A-000001", patient.PatientHN)
	assert.Equal(t, "Somchai", patient.FirstNameEN)
	assert.Equal(t, "ใจดี", patient.LastNameTH)
	assert.Equal(t, domain.GenderMale, patient.Gender)
	require.NotNil(t, patient.DateOfBirth)
	assert.Equal(t, "1985-04-12", patient.DateOfBirth.Format(his.DateLayout))
	assert.Zero(t, patient.HospitalID, "the caller assigns the hospital")
}

func TestHTTPClient_FindPatient_MatchesPassportCaseInsensitively(t *testing.T) {
	server := newFakeHIS(t, func(w http.ResponseWriter, _ string) {
		writeJSON(t, w, http.StatusOK, his.PatientPayload{PatientHN: "HN-A-000003", PassportID: "AA1234567"})
	})
	client := his.NewHTTPClient(server.URL, time.Second)

	patient, err := client.FindPatient(context.Background(), "aa1234567")

	require.NoError(t, err)
	assert.Equal(t, "AA1234567", patient.PassportID)
}

func TestHTTPClient_FindPatient_Errors(t *testing.T) {
	testCases := []struct {
		name          string
		handle        func(t *testing.T, w http.ResponseWriter)
		expectedError error
		errorContains string
	}{
		{
			name: "404 means patient not found",
			handle: func(t *testing.T, w http.ResponseWriter) {
				writeJSON(t, w, http.StatusNotFound, map[string]string{"error": "not found"})
			},
			expectedError: domain.ErrPatientNotFound,
		},
		{
			name: "5xx is reported as an HIS failure",
			handle: func(t *testing.T, w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadGateway)
			},
			errorContains: "unexpected status 502",
		},
		{
			name: "invalid JSON",
			handle: func(t *testing.T, w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{not json"))
			},
			errorContains: "decode response",
		},
		{
			name: "missing HN violates the contract",
			handle: func(t *testing.T, w http.ResponseWriter) {
				payload := somchaiPayload()
				payload.PatientHN = ""
				writeJSON(t, w, http.StatusOK, payload)
			},
			errorContains: "patient_hn is missing",
		},
		{
			name: "record for a different person is rejected",
			handle: func(t *testing.T, w http.ResponseWriter) {
				payload := somchaiPayload()
				payload.NationalID = "1100000000024"
				writeJSON(t, w, http.StatusOK, payload)
			},
			errorContains: "does not match",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newFakeHIS(t, func(w http.ResponseWriter, _ string) { testCase.handle(t, w) })
			client := his.NewHTTPClient(server.URL, time.Second)

			_, err := client.FindPatient(context.Background(), somchaiNationalID)

			require.Error(t, err)
			if testCase.expectedError != nil {
				assert.ErrorIs(t, err, testCase.expectedError)
			}
			if testCase.errorContains != "" {
				assert.Contains(t, err.Error(), testCase.errorContains)
			}
		})
	}
}

func TestHTTPClient_FindPatient_TimeoutErrorDoesNotLeakIdentityNumber(t *testing.T) {
	server := newFakeHIS(t, func(w http.ResponseWriter, _ string) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(t, w, http.StatusOK, somchaiPayload())
	})
	client := his.NewHTTPClient(server.URL, 20*time.Millisecond)

	_, err := client.FindPatient(context.Background(), somchaiNationalID)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), somchaiNationalID, "errors are logged, so they must not contain the national ID")
}

func TestHTTPClient_FindPatient_UnreachableHIS(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	unreachableURL := server.URL
	server.Close()
	client := his.NewHTTPClient(unreachableURL, time.Second)

	_, err := client.FindPatient(context.Background(), somchaiNationalID)

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "his: request failed"))
	assert.NotContains(t, err.Error(), somchaiNationalID)
}

func TestRegistry_ClientFor(t *testing.T) {
	registry := his.NewHTTPRegistry(map[string]string{"Hospital-A": "http://his-a.local"}, time.Second)

	client, found := registry.ClientFor("hospital-a")
	assert.True(t, found)
	assert.NotNil(t, client)

	_, found = registry.ClientFor("hospital-b")
	assert.False(t, found)
}
