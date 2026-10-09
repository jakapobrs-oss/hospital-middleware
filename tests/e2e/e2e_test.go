//go:build e2e

// End-to-end tests against the running docker compose stack, through Nginx:
//
//	docker compose up --build -d
//	go test -count=1 -tags e2e ./tests/e2e/...        # E2E_BASE_URL defaults to http://localhost:8080
//
// They rely on the synthetic demo data (SEED_DEMO_DATA=true) and the mock Hospital A HIS.
// Usernames get a unique suffix, so the suite can run repeatedly against the same stack.
package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const strongPassword = "S3cure-Passw0rd"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func baseURL() string {
	if value := os.Getenv("E2E_BASE_URL"); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "http://localhost:8080"
}

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func (response apiResponse) decode(t *testing.T, target any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(response.body, target), string(response.body))
}

func (response apiResponse) errorCode(t *testing.T) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	response.decode(t, &envelope)
	return envelope.Error.Code
}

func call(t *testing.T, method, path, token string, body any) apiResponse {
	t.Helper()
	rawBody := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		rawBody = string(encoded)
	}
	return callRaw(t, method, path, token, rawBody)
}

// callRaw sends rawBody exactly as given (as JSON when not empty).
func callRaw(t *testing.T, method, path, token, rawBody string) apiResponse {
	t.Helper()
	var requestBody io.Reader
	if rawBody != "" {
		requestBody = bytes.NewReader([]byte(rawBody))
	}
	req, err := http.NewRequest(method, baseURL()+path, requestBody)
	require.NoError(t, err)
	if rawBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := httpClient.Do(req)
	require.NoError(t, err, "is the stack running? docker compose up --build -d")
	defer res.Body.Close()
	responseBody, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return apiResponse{status: res.StatusCode, header: res.Header, body: responseBody}
}

type searchResult struct {
	Data []struct {
		PatientHN   string  `json:"patient_hn"`
		NationalID  *string `json:"national_id"`
		PassportID  *string `json:"passport_id"`
		FirstNameTH *string `json:"first_name_th"`
		FirstNameEN *string `json:"first_name_en"`
		DateOfBirth *string `json:"date_of_birth"`
		Gender      *string `json:"gender"`
	} `json:"data"`
	Pagination struct {
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	} `json:"pagination"`
}

func (result searchResult) hns() []string {
	hns := []string{}
	for _, patient := range result.Data {
		hns = append(hns, patient.PatientHN)
	}
	return hns
}

func search(t *testing.T, token string, filters map[string]any) searchResult {
	t.Helper()
	response := call(t, http.MethodPost, "/patient/search", token, filters)
	require.Equal(t, http.StatusOK, response.status, string(response.body))
	var result searchResult
	response.decode(t, &result)
	return result
}

func login(t *testing.T, username, hospital string) string {
	t.Helper()
	response := call(t, http.MethodPost, "/staff/login", "", map[string]string{
		"username": username, "password": strongPassword, "hospital": hospital,
	})
	require.Equal(t, http.StatusOK, response.status, string(response.body))
	var body struct {
		AccessToken string `json:"access_token"`
	}
	response.decode(t, &body)
	require.NotEmpty(t, body.AccessToken)
	return body.AccessToken
}

func TestEndToEnd(t *testing.T) {
	username := fmt.Sprintf("e2e.nurse.%d", time.Now().UnixNano())

	t.Run("health", func(t *testing.T) {
		response := call(t, http.MethodGet, "/health", "", nil)
		assert.Equal(t, http.StatusOK, response.status)
		assert.Equal(t, "nosniff", response.header.Get("X-Content-Type-Options"), "nginx security headers")
	})

	t.Run("staff create", func(t *testing.T) {
		response := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username, "password": strongPassword, "hospital": "hospital-a",
		})
		require.Equal(t, http.StatusCreated, response.status, string(response.body))
		var created struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Hospital struct {
				Code string `json:"code"`
			} `json:"hospital"`
		}
		response.decode(t, &created)
		assert.Positive(t, created.ID)
		assert.Equal(t, username, created.Username)
		assert.Equal(t, "hospital-a", created.Hospital.Code)
		assert.NotContains(t, string(response.body), "password", "no password or hash in the response")

		duplicate := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username, "password": strongPassword, "hospital": "hospital-a",
		})
		assert.Equal(t, http.StatusConflict, duplicate.status)
		assert.Equal(t, "username_taken", duplicate.errorCode(t))

		sameNameOtherHospital := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username, "password": strongPassword, "hospital": "hospital-b",
		})
		assert.Equal(t, http.StatusCreated, sameNameOtherHospital.status, "usernames are unique per hospital only")

		unknownHospital := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username + ".x", "password": strongPassword, "hospital": "hospital-z",
		})
		assert.Equal(t, http.StatusBadRequest, unknownHospital.status)
		assert.Equal(t, "unknown_hospital", unknownHospital.errorCode(t))

		weakPassword := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username + ".y", "password": "short", "hospital": "hospital-a",
		})
		assert.Equal(t, http.StatusBadRequest, weakPassword.status)
		assert.Equal(t, "validation_error", weakPassword.errorCode(t))

		// Hospitals can be named the way people write them, and usernames may be e-mail addresses.
		byHospitalName := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username + "@example.com", "password": strongPassword, "hospital": "Hospital A",
		})
		require.Equal(t, http.StatusCreated, byHospitalName.status, string(byHospitalName.body))
		assert.Contains(t, string(byHospitalName.body), `"code":"hospital-a"`)
	})

	t.Run("login never accepts a password longer than bcrypt can check", func(t *testing.T) {
		longPassword := strings.Repeat("a", 72)
		created := call(t, http.MethodPost, "/staff/create", "", map[string]string{
			"username": username + ".long", "password": longPassword, "hospital": "hospital-a",
		})
		require.Equal(t, http.StatusCreated, created.status, string(created.body))

		// bcrypt only reads 72 bytes, so without a guard this different password would log in.
		failed := call(t, http.MethodPost, "/staff/login", "", map[string]string{
			"username": username + ".long", "password": longPassword + "x", "hospital": "hospital-a",
		})
		assert.Equal(t, http.StatusUnauthorized, failed.status)
	})

	t.Run("staff login", func(t *testing.T) {
		response := call(t, http.MethodPost, "/staff/login", "", map[string]string{
			"username": strings.ToUpper(username), "password": strongPassword, "hospital": "HOSPITAL-A",
		})
		require.Equal(t, http.StatusOK, response.status, string(response.body))
		assert.Equal(t, "no-store", response.header.Get("Cache-Control"))
		var body struct {
			TokenType string `json:"token_type"`
			ExpiresIn int    `json:"expires_in"`
		}
		response.decode(t, &body)
		assert.Equal(t, "Bearer", body.TokenType)
		assert.InDelta(t, 3600, body.ExpiresIn, 5)

		for name, credentials := range map[string]map[string]string{
			"wrong password":   {"username": username, "password": "Wrong-Passw0rd", "hospital": "hospital-a"},
			"unknown user":     {"username": "nobody." + username, "password": strongPassword, "hospital": "hospital-a"},
			"unknown hospital": {"username": username, "password": strongPassword, "hospital": "hospital-z"},
		} {
			failed := call(t, http.MethodPost, "/staff/login", "", credentials)
			assert.Equal(t, http.StatusUnauthorized, failed.status, name)
			assert.Equal(t, "invalid_credentials", failed.errorCode(t), name)
		}
	})

	tokenA := login(t, username, "hospital-a")
	tokenB := login(t, username, "hospital-b")

	t.Run("patient search requires a valid token", func(t *testing.T) {
		missing := call(t, http.MethodPost, "/patient/search", "", map[string]any{})
		assert.Equal(t, http.StatusUnauthorized, missing.status)
		assert.Equal(t, "unauthorized", missing.errorCode(t))

		tampered := call(t, http.MethodPost, "/patient/search", tokenA[:len(tokenA)-2]+"xx", map[string]any{})
		assert.Equal(t, http.StatusUnauthorized, tampered.status)
	})

	t.Run("each hospital sees only its own patients", func(t *testing.T) {
		assert.Equal(t, []string{"HN-A-000001", "HN-A-000002"}, search(t, tokenA, map[string]any{"first_name": "som"}).hns())
		assert.Equal(t, []string{"HN-B-000001"}, search(t, tokenB, map[string]any{"first_name": "som"}).hns())

		// Hospital B staff cannot reach Hospital A's record of the same person, nor its HIS.
		assert.Equal(t, []string{"HN-B-000001"}, search(t, tokenB, map[string]any{"national_id": "1100000000016"}).hns())
		assert.Empty(t, search(t, tokenB, map[string]any{"national_id": "1100000000032"}).hns())
	})

	t.Run("search by national ID pulls the record from the HIS", func(t *testing.T) {
		// HN-A-000004 exists only in the mock HIS until someone searches for its national ID.
		alreadyStored := len(search(t, tokenA, map[string]any{"first_name": "WICH"}).Data) > 0

		result := search(t, tokenA, map[string]any{"national_id": "1-1000-00000-03-2"})
		require.Equal(t, []string{"HN-A-000004"}, result.hns())
		assert.Equal(t, "Wichai", *result.Data[0].FirstNameEN)

		if alreadyStored {
			t.Log("HN-A-000004 was stored by an earlier run; recreate the stack (docker compose down -v) to prove the HIS fetch itself")
			return
		}
		// It was not in the middleware before, so it can only have come from the HIS; now a name search finds it too.
		assert.Equal(t, []string{"HN-A-000004"}, search(t, tokenA, map[string]any{"first_name": "WICH"}).hns())
	})

	t.Run("lookup by identity number, shaped like the HIS route", func(t *testing.T) {
		for identityNumber, expectedHN := range map[string]string{"1100000000016": "HN-A-000001", "aa1234567": "HN-A-000003"} {
			response := call(t, http.MethodGet, "/patient/search/"+identityNumber, tokenA, nil)
			require.Equal(t, http.StatusOK, response.status, string(response.body))
			var patient struct {
				PatientHN string `json:"patient_hn"`
			}
			response.decode(t, &patient)
			assert.Equal(t, expectedHN, patient.PatientHN)
		}

		unknown := call(t, http.MethodGet, "/patient/search/1100000000099", tokenA, nil)
		assert.Equal(t, http.StatusNotFound, unknown.status)
		otherHospital := call(t, http.MethodGet, "/patient/search/1100000000032", tokenB, nil)
		assert.Equal(t, http.StatusNotFound, otherHospital.status, "Hospital B cannot look up Hospital A's patient")
	})

	t.Run("input that would silently widen the search is rejected", func(t *testing.T) {
		for name, request := range map[string]struct{ method, path, body string }{
			"misspelt JSON field":     {http.MethodPost, "/patient/search", `{"firstName":"som"}`},
			"unknown query parameter": {http.MethodGet, "/patient/search?name=som", ""},
			"second JSON object":      {http.MethodPost, "/patient/search", `{}{"national_id":"1100000000032"}`},
			"NUL character":           {http.MethodPost, "/patient/search", `{"first_name":"\u0000"}`},
		} {
			response := callRaw(t, request.method, request.path, tokenA, request.body)
			assert.Equal(t, http.StatusBadRequest, response.status, name)
		}
	})

	t.Run("every filter", func(t *testing.T) {
		testCases := map[string]struct {
			filters     map[string]any
			expectedHNs []string
		}{
			"thai last name":         {map[string]any{"last_name": "ใจดี"}, []string{"HN-A-000001"}},
			"thai middle name":       {map[string]any{"middle_name": "มณี"}, []string{"HN-A-000002"}},
			"english middle name":    {map[string]any{"middle_name": "michael"}, []string{"HN-A-000003"}},
			"passport, any case":     {map[string]any{"passport_id": "aa1234567"}, []string{"HN-A-000003"}},
			"date of birth":          {map[string]any{"date_of_birth": "1985-04-12"}, []string{"HN-A-000001"}},
			"phone, digits only":     {map[string]any{"phone_number": "0800000002"}, []string{"HN-A-000002"}},
			"phone, +66 form":        {map[string]any{"phone_number": "+66 80 000 0002"}, []string{"HN-A-000002"}},
			"email, any case":        {map[string]any{"email": "SOMYING.R@EXAMPLE.COM"}, []string{"HN-A-000002"}},
			"filters combine (AND)":  {map[string]any{"first_name": "som", "last_name": "rakthai"}, []string{"HN-A-000002"}},
			"no match is empty list": {map[string]any{"first_name": "zzz"}, []string{}},
		}
		for name, testCase := range testCases {
			t.Run(name, func(t *testing.T) {
				assert.Equal(t, testCase.expectedHNs, search(t, tokenA, testCase.filters).hns())
			})
		}
	})

	t.Run("no filter lists the hospital page by page", func(t *testing.T) {
		everyone := search(t, tokenA, map[string]any{})
		assert.Equal(t, len(everyone.Data), everyone.Pagination.Total)
		assert.Equal(t, 20, everyone.Pagination.Limit)
		for _, hn := range everyone.hns() {
			assert.True(t, strings.HasPrefix(hn, "HN-A-"), hn)
		}

		page := search(t, tokenA, map[string]any{"limit": 1, "offset": 1})
		assert.Len(t, page.Data, 1)
		assert.Equal(t, everyone.Pagination.Total, page.Pagination.Total)
		assert.Equal(t, everyone.Data[1].PatientHN, page.Data[0].PatientHN)
	})

	t.Run("GET with query parameters works too", func(t *testing.T) {
		response := call(t, http.MethodGet, "/patient/search?last_name=jaidee&limit=5", tokenA, nil)
		require.Equal(t, http.StatusOK, response.status, string(response.body))
		var result searchResult
		response.decode(t, &result)
		assert.Equal(t, []string{"HN-A-000001"}, result.hns())
		assert.Equal(t, 5, result.Pagination.Limit)
	})

	t.Run("invalid search input", func(t *testing.T) {
		for name, filters := range map[string]map[string]any{
			"bad date":   {"date_of_birth": "12/04/1985"},
			"bad limit":  {"limit": 1000},
			"short ID":   {"national_id": "12345"},
			"wrong type": {"limit": "ten"},
		} {
			response := call(t, http.MethodPost, "/patient/search", tokenA, filters)
			assert.Equal(t, http.StatusBadRequest, response.status, name)
			assert.Equal(t, "validation_error", response.errorCode(t), name)
		}
	})

	t.Run("unknown route", func(t *testing.T) {
		response := call(t, http.MethodGet, "/patients", tokenA, nil)
		assert.Equal(t, http.StatusNotFound, response.status)
		assert.Equal(t, "not_found", response.errorCode(t))
	})
}
