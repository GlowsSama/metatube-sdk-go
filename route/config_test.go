package route_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/route"
	"github.com/metatube-community/metatube-sdk-go/route/auth"
)

const testToken = "test-token"

type flareSolverrResponse struct {
	Data struct {
		Endpoint  string   `json:"endpoint"`
		Providers []string `json:"providers"`
	} `json:"data"`
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestFlareSolverrConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := engine.New(nil)
	router := route.New(app, auth.Token(testToken))

	send := func(body, token string) (*httptest.ResponseRecorder, flareSolverrResponse) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/v1/config/flaresolverr",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		var response flareSolverrResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		return rec, response
	}

	t.Run("requires authentication", func(t *testing.T) {
		rec, response := send(`{"endpoint":"http://flaresolverr:8191/v1"}`, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, http.StatusUnauthorized, response.Error.Code)
	})

	t.Run("configures capable providers", func(t *testing.T) {
		rec, response := send(`{"endpoint":"http://flaresolverr:8191/v1"}`, testToken)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "http://flaresolverr:8191/v1", response.Data.Endpoint)
		assert.Contains(t, response.Data.Providers, "JavDB")
	})

	t.Run("rejects invalid endpoint", func(t *testing.T) {
		rec, response := send(`{"endpoint":"localhost:8191"}`, testToken)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, http.StatusBadRequest, response.Error.Code)
		assert.Contains(t, response.Error.Message, "absolute HTTP URL")
	})

	t.Run("empty endpoint disables FlareSolverr", func(t *testing.T) {
		rec, response := send(`{"endpoint":""}`, testToken)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Empty(t, response.Data.Endpoint)
		assert.NotEmpty(t, response.Data.Providers)
	})
}
