package flaresolverr

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func jsonResponse(value any) *http.Response {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(&body),
	}
}

func TestTransportFallsBackOnCloudflareChallenge(t *testing.T) {
	direct := roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := response(http.StatusForbidden, "")
		resp.Header.Set("Cf-Mitigated", "challenge")
		return resp, nil
	})

	var payload apiRequest
	solver, err := NewClient("http://flaresolverr.local", 5*time.Second)
	require.NoError(t, err)
	solver.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "http://flaresolverr.local/v1", req.URL.String())
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		return jsonResponse(apiResponse{
			Status: "ok",
			Solution: &solution{
				URL:    "https://javdb.com/search?q=FC2-1",
				Status: http.StatusOK,
				Headers: map[string]string{
					"Content-Type": "text/html; charset=utf-8",
				},
				Response: "<html>solved</html>",
				Cookies: []cookie{{
					Name:     "cf_clearance",
					Value:    "token",
					Domain:   "example.com",
					Path:     "/",
					Expires:  time.Now().Add(time.Hour).Unix(),
					Secure:   true,
					HTTPOnly: true,
				}},
			},
		}), nil
	})

	transport := NewTransport(direct)
	transport.solver = solver
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	resp, err := client.Get("https://javdb.com/search?q=FC2-1")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "<html>solved</html>", string(body))
	assert.Equal(t, "request.get", payload.Cmd)
	assert.Equal(t, "https://javdb.com/search?q=FC2-1", payload.URL)
	assert.Positive(t, payload.MaxTimeout)
	assert.Contains(t, resp.Header.Get("Set-Cookie"), "cf_clearance=token")
}

func TestTransportDoesNotCallFlareSolverrForNormalResponse(t *testing.T) {
	direct := roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := response(http.StatusOK, "<html>normal</html>")
		resp.Header.Set("Content-Type", "text/html")
		return resp, nil
	})

	var called atomic.Bool
	transport := NewTransport(direct)
	transport.solver = &Client{}
	transport.solver.httpClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return nil, nil
		}),
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	resp, err := client.Get("https://example.com")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "<html>normal</html>", string(body))
	assert.False(t, called.Load())
}

func TestTransportDetectsChallengePageWithHTTP200(t *testing.T) {
	direct := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK,
			`<html><title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/x"></script></html>`), nil
	})

	solver, err := NewClient("http://flaresolverr.local/v1/", 5*time.Second)
	require.NoError(t, err)
	solver.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(apiResponse{
			Status: "ok",
			Solution: &solution{
				Status:   http.StatusOK,
				Response: "<html>solved after 200 challenge</html>",
			},
		}), nil
	})

	transport := NewTransport(direct)
	transport.solver = solver
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	resp, err := client.Get("https://example.com")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "<html>solved after 200 challenge</html>", string(body))
}

func TestTransportSurfacesFlareSolverrErrors(t *testing.T) {
	direct := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ""), nil
	})

	solver, err := NewClient("http://flaresolverr.local", 5*time.Second)
	require.NoError(t, err)
	solver.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(apiResponse{
			Status:  "error",
			Message: "challenge failed",
		}), nil
	})

	transport := NewTransport(direct)
	transport.solver = solver
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	_, err = client.Get("https://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "challenge failed")
}

func TestTransportReusesSolvedUserAgent(t *testing.T) {
	var calls atomic.Int32
	direct := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return response(http.StatusForbidden, ""), nil
		}
		assert.Equal(t, "FlareSolverr Browser", req.Header.Get("User-Agent"))
		return response(http.StatusOK, "<html>normal</html>"), nil
	})

	solver, err := NewClient("http://flaresolverr.local", 5*time.Second)
	require.NoError(t, err)
	solver.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(apiResponse{
			Status: "ok",
			Solution: &solution{
				Status:    http.StatusOK,
				Response:  "<html>solved</html>",
				UserAgent: "FlareSolverr Browser",
			},
		}), nil
	})

	transport := NewTransport(direct)
	transport.solver = solver
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for range 2 {
		resp, err := client.Get("https://example.com")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
}

func TestNormalizeEndpointRejectsInvalidURL(t *testing.T) {
	for _, endpoint := range []string{"", "localhost:8191", "ftp://localhost:8191"} {
		_, err := normalizeEndpoint(endpoint)
		assert.Error(t, err)
	}
}
