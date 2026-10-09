// Command mockhis is a stand-in for the Hospital A HIS (https://hospital-a.api.co.th), which is not reachable.
// It implements GET /patient/search/{id} with synthetic patients so the middleware can be demoed end to end.
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jakapobrs-oss/hospital-middleware/internal/demodata"
	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
	"github.com/jakapobrs-oss/hospital-middleware/internal/his"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	port := os.Getenv("MOCK_HIS_PORT")
	if port == "" {
		port = "8081"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           newMockHISHandler(demodata.HospitalAHISPatients()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("mock HIS listening", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("mock HIS stopped", "error", err)
		os.Exit(1)
	}
}

// newMockHISHandler serves the Hospital A HIS contract from an in-memory list.
func newMockHISHandler(patients []domain.Patient) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /patient/search/{id}", func(w http.ResponseWriter, r *http.Request) {
		identityNumber := r.PathValue("id")
		for _, patient := range patients {
			if his.MatchesIdentityNumber(patient, identityNumber) {
				writeJSON(w, http.StatusOK, his.PayloadFromDomain(patient))
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "patient not found"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
