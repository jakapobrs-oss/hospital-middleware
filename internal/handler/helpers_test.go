package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
)

// Shared helpers for every handler test in this package.

func init() {
	gin.SetMode(gin.TestMode)
}

// performRequest sends a request through the engine; a non-empty body is sent as JSON.
func performRequest(engine *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	var requestBody io.Reader
	if body != "" {
		requestBody = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, target, requestBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

// decodeErrorBody parses the standard error envelope.
func decodeErrorBody(t *testing.T, recorder *httptest.ResponseRecorder) apierror.Body {
	t.Helper()
	var body apierror.Body
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	return body
}
