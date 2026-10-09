package his

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

// maxResponseBytes caps how much of an HIS response is read, so a misbehaving HIS cannot exhaust memory.
const maxResponseBytes = 1 << 20

// HTTPClient calls an HIS that implements the Hospital A API: GET {baseURL}/patient/search/{id}.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPClient creates a client for the HIS at baseURL. Every call is bounded by timeout.
func NewHTTPClient(baseURL string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

// NewHTTPRegistry builds a registry with one HTTPClient per configured hospital code.
func NewHTTPRegistry(baseURLsByHospitalCode map[string]string, timeout time.Duration) *Registry {
	clients := make(map[string]Client, len(baseURLsByHospitalCode))
	for hospitalCode, baseURL := range baseURLsByHospitalCode {
		clients[hospitalCode] = NewHTTPClient(baseURL, timeout)
	}
	return NewRegistry(clients)
}

// FindPatient implements Client.
func (client *HTTPClient) FindPatient(ctx context.Context, identityNumber string) (domain.Patient, error) {
	endpoint := client.baseURL + "/patient/search/" + url.PathEscape(identityNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.Patient{}, fmt.Errorf("his: build request: %w", withoutURL(err))
	}
	req.Header.Set("Accept", "application/json")

	res, err := client.httpClient.Do(req)
	if err != nil {
		return domain.Patient{}, fmt.Errorf("his: request failed: %w", withoutURL(err))
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return domain.Patient{}, domain.ErrPatientNotFound
	default:
		return domain.Patient{}, fmt.Errorf("his: unexpected status %d", res.StatusCode)
	}

	var payload PatientPayload
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return domain.Patient{}, fmt.Errorf("his: decode response: %w", err)
	}
	patient, err := payload.ToDomain()
	if err != nil {
		return domain.Patient{}, err
	}

	// Never cache a record that does not belong to the identity number that was asked for.
	if !MatchesIdentityNumber(patient, identityNumber) {
		return domain.Patient{}, errors.New("his: returned patient does not match the requested identity number")
	}
	return patient, nil
}

// MatchesIdentityNumber reports whether identityNumber is the patient's national ID or passport ID.
func MatchesIdentityNumber(patient domain.Patient, identityNumber string) bool {
	if patient.NationalID != "" && patient.NationalID == domain.NormalizeNationalID(identityNumber) {
		return true
	}
	return patient.PassportID != "" && patient.PassportID == domain.NormalizePassportID(identityNumber)
}

// withoutURL strips the request URL from transport errors: the URL contains a national ID or
// passport number, and these errors end up in logs.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
