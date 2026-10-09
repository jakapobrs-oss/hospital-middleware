package apierror_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/apierror"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type sampleRequest struct {
	Name     string   `json:"name" binding:"required,min=2,max=5"`
	Code     string   `json:"code" binding:"omitempty,len=3"`
	Email    string   `json:"email" binding:"omitempty,email"`
	Digits   string   `json:"digits" binding:"omitempty,numeric"`
	Born     string   `json:"born" binding:"omitempty,datetime=2006-01-02"`
	Color    string   `json:"color" binding:"omitempty,oneof=red blue"`
	Count    int      `form:"count" binding:"omitempty,max=10"`
	Website  string   `json:"website" binding:"omitempty,url"`
	Unnamed  string   `binding:"omitempty,max=1"`
	Excluded []string `json:"-"`
}

// respondTo runs fn inside a Gin handler and returns the recorded response.
func respondTo(fn func(c *gin.Context)) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	fn(c)
	return recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) apierror.Body {
	t.Helper()
	var body apierror.Body
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestRespond_WritesEnvelopeAndAborts(t *testing.T) {
	var aborted bool
	recorder := respondTo(func(c *gin.Context) {
		apierror.Respond(c, http.StatusConflict, apierror.CodeUsernameTaken, "taken")
		aborted = c.IsAborted()
	})

	assert.True(t, aborted)
	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.JSONEq(t, `{"error":{"code":"username_taken","message":"taken"}}`, recorder.Body.String())
}

func TestRespondValidation_UsesClientFieldNamesAndReadableMessages(t *testing.T) {
	request := sampleRequest{
		Name:    "x",
		Code:    "ab",
		Email:   "not-an-email",
		Digits:  "12a",
		Born:    "12/04/1985",
		Color:   "green",
		Count:   11,
		Website: "nope",
		Unnamed: "too long",
	}
	validationErr := binding.Validator.ValidateStruct(&request)
	require.Error(t, validationErr)

	recorder := respondTo(func(c *gin.Context) { apierror.RespondValidation(c, validationErr) })

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	body := decode(t, recorder)
	assert.Equal(t, apierror.CodeValidation, body.Error.Code)

	messagesByField := map[string]string{}
	for _, fieldError := range body.Error.Fields {
		messagesByField[fieldError.Field] = fieldError.Message
	}
	assert.Equal(t, map[string]string{
		"name":    "must be at least 2 characters",
		"code":    "must be exactly 3 characters",
		"email":   "must be a valid email address",
		"digits":  "must contain digits only",
		"born":    "must be a date in YYYY-MM-DD format",
		"color":   "must be one of: red blue",
		"count":   "must be at most 10",
		"website": "is invalid (url)",
		"Unnamed": "must be at most 1 characters",
	}, messagesByField)
}

func TestRespondValidation_RequiredField(t *testing.T) {
	validationErr := binding.Validator.ValidateStruct(&sampleRequest{})
	recorder := respondTo(func(c *gin.Context) { apierror.RespondValidation(c, validationErr) })

	body := decode(t, recorder)
	require.Len(t, body.Error.Fields, 1)
	assert.Equal(t, apierror.FieldError{Field: "name", Message: "is required"}, body.Error.Fields[0])
}

func TestRespondValidation_MalformedInput(t *testing.T) {
	recorder := respondTo(func(c *gin.Context) { apierror.RespondValidation(c, errors.New("unexpected EOF")) })

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	body := decode(t, recorder)
	assert.Equal(t, apierror.CodeValidation, body.Error.Code)
	assert.Empty(t, body.Error.Fields)
}

func TestRespondValidation_BodyTooLarge(t *testing.T) {
	tooLarge := &http.MaxBytesError{Limit: 1 << 20}

	recorder := respondTo(func(c *gin.Context) { apierror.RespondValidation(c, fmt.Errorf("decode: %w", tooLarge)) })

	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Equal(t, apierror.CodePayloadTooLarge, decode(t, recorder).Error.Code)
}

func TestRespondFields(t *testing.T) {
	recorder := respondTo(func(c *gin.Context) {
		apierror.RespondFields(c, apierror.FieldError{Field: "national_id", Message: "must contain 13 digits"})
	})

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.JSONEq(t, `{"error":{"code":"validation_error","message":"request has invalid fields",
		"fields":[{"field":"national_id","message":"must contain 13 digits"}]}}`, recorder.Body.String())
}
