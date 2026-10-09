package handler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/jakapobrs-oss/hospital-middleware/internal/handler"
)

type fakeDatabase struct {
	pingErr error
}

func (database fakeDatabase) Ping(context.Context) error {
	return database.pingErr
}

func TestHealthHandler_Check(t *testing.T) {
	testCases := []struct {
		name           string
		pingErr        error
		expectedStatus int
		expectedBody   string
	}{
		{name: "database reachable", expectedStatus: http.StatusOK, expectedBody: `{"status":"ok"}`},
		{name: "database down", pingErr: errors.New("connection refused"), expectedStatus: http.StatusServiceUnavailable, expectedBody: `{"status":"unavailable"}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			engine := gin.New()
			engine.GET("/health", handler.NewHealthHandler(fakeDatabase{pingErr: testCase.pingErr}).Check)

			recorder := performRequest(engine, http.MethodGet, "/health", "")

			assert.Equal(t, testCase.expectedStatus, recorder.Code)
			assert.JSONEq(t, testCase.expectedBody, recorder.Body.String())
		})
	}
}
