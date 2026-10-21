package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout      = 60 * time.Second
	challengeProbeLimit = 64 * 1024
	maxAPIResponseSize  = 16 * 1024 * 1024
)

// Client communicates with the FlareSolverr HTTP API.
type Client struct {
	endpoint *url.URL

	mu         sync.RWMutex
	timeout    time.Duration
	httpClient *http.Client
}

type apiRequest struct {
	Cmd        string            `json:"cmd"`
	URL        string            `json:"url"`
	MaxTimeout int64             `json:"maxTimeout"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type apiResponse struct {
	Status   string    `json:"status"`
	Message  string    `json:"message"`
	Solution *solution `json:"solution"`
}

type solution struct {
	URL       string            `json:"url"`
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Response  string            `json:"response"`
	Cookies   []cookie          `json:"cookies"`
	UserAgent string            `json:"userAgent"`
}

type cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
}

// NewClient creates a FlareSolverr client. The endpoint may include or omit
// the /v1 suffix.
func NewClient(endpoint string, timeout time.Duration) (*Client, error) {
	u, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		endpoint: u,
		timeout:  timeout,
		httpClient: &http.Client{
			Timeout: timeout + 15*time.Second,
		},
	}, nil
}

func normalizeEndpoint(endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("empty FlareSolverr endpoint")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse FlareSolverr endpoint: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("FlareSolverr endpoint must be an absolute HTTP URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

// SetTimeout updates the maximum challenge-solving time.
func (c *Client) SetTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	c.mu.Lock()
	c.timeout = timeout
	c.httpClient = &http.Client{
		Timeout: timeout + 15*time.Second,
	}
	c.mu.Unlock()
}

func (c *Client) solve(ctx context.Context, method, target string, headers http.Header) (*solution, error) {
	c.mu.RLock()
	httpClient := c.httpClient
	timeout := c.timeout
	c.mu.RUnlock()

	maxTimeout := timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < maxTimeout {
			maxTimeout = remaining
		}
	}
	if maxTimeout <= 0 {
		return nil, fmt.Errorf("FlareSolverr request deadline exceeded")
	}

	cmd := "request.get"
	if method == http.MethodPost {
		cmd = "request.post"
	}
	payload := apiRequest{
		Cmd:        cmd,
		URL:        target,
		MaxTimeout: maxTimeout.Milliseconds(),
		Headers:    solverHeaders(headers),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode FlareSolverr request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create FlareSolverr request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call FlareSolverr: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FlareSolverr returned HTTP %d", resp.StatusCode)
	}
	var result apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseSize)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode FlareSolverr response: %w", err)
	}
	if !strings.EqualFold(result.Status, "ok") {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "unknown error"
		}
		return nil, fmt.Errorf("FlareSolverr status %q: %s", result.Status, message)
	}
	if result.Solution == nil {
		return nil, fmt.Errorf("FlareSolverr response has no solution")
	}
	if result.Solution.Status < 100 || result.Solution.Status > 599 {
		return nil, fmt.Errorf("FlareSolverr solution has invalid HTTP status %d", result.Solution.Status)
	}
	return result.Solution, nil
}

func solverHeaders(headers http.Header) map[string]string {
	result := make(map[string]string)
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		switch strings.ToLower(key) {
		case "connection", "content-length", "cookie", "host", "proxy-connection",
			"te", "trailer", "transfer-encoding", "upgrade":
			continue
		default:
			result[key] = values[0]
		}
	}
	return result
}

// Transport first requests the target directly and falls back to FlareSolverr
// only when Cloudflare blocks or challenges the request.
type Transport struct {
	mu        sync.RWMutex
	base      http.RoundTripper
	solver    *Client
	timeout   time.Duration
	userAgent string
}

// NewTransport creates a transport around an optional base transport.
func NewTransport(base http.RoundTripper) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{
		base:    base,
		timeout: defaultTimeout,
	}
}

// SetBase changes the transport used for direct requests.
func (t *Transport) SetBase(base http.RoundTripper) {
	if base == nil {
		base = http.DefaultTransport
	}
	t.mu.Lock()
	t.base = base
	t.mu.Unlock()
}

// Base returns the transport used for direct requests.
func (t *Transport) Base() http.RoundTripper {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.base
}

// SetEndpoint enables FlareSolverr. An empty endpoint disables it.
func (t *Transport) SetEndpoint(endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		t.mu.Lock()
		t.solver = nil
		t.mu.Unlock()
		return nil
	}

	t.mu.RLock()
	timeout := t.timeout
	t.mu.RUnlock()
	solver, err := NewClient(endpoint, timeout)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.solver = solver
	t.mu.Unlock()
	return nil
}

// SetTimeout updates the FlareSolverr timeout.
func (t *Transport) SetTimeout(timeout time.Duration) {
	t.mu.Lock()
	t.timeout = timeout
	solver := t.solver
	t.mu.Unlock()
	if solver != nil {
		solver.SetTimeout(timeout)
	}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	base := t.base
	solver := t.solver
	userAgent := t.userAgent
	t.mu.RUnlock()

	if userAgent != "" && req.Header.Get("User-Agent") != userAgent {
		req = req.Clone(req.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := base.RoundTrip(req)
	if err != nil || solver == nil ||
		(req.Method != http.MethodGet && req.Method != http.MethodPost) {
		return resp, err
	}
	if !isChallenge(resp) {
		return resp, nil
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	solved, err := solver.solve(req.Context(), req.Method, req.URL.String(), req.Header)
	if err != nil {
		return nil, fmt.Errorf("solve %s with FlareSolverr: %w", req.URL, err)
	}
	if solved.UserAgent != "" {
		t.mu.Lock()
		t.userAgent = solved.UserAgent
		t.mu.Unlock()
	}
	return solved.response(req), nil
}

func isChallenge(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	}
	if resp.StatusCode >= 520 && resp.StatusCode <= 527 {
		return true
	}
	for key, values := range resp.Header {
		if !strings.EqualFold(key, "Cf-Mitigated") {
			continue
		}
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(value), "challenge") {
				return true
			}
		}
	}
	if resp.Body == nil {
		return false
	}

	prefix, _ := io.ReadAll(io.LimitReader(resp.Body, challengeProbeLimit))
	resp.Body = &readCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body),
		Closer: resp.Body,
	}
	lower := strings.ToLower(string(prefix))
	for _, marker := range []string{
		"just a moment...",
		"attention required! | cloudflare",
		"cf-browser-verification",
		"cf-chl",
		"challenge-platform",
		"__cf_chl_",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

type readCloser struct {
	io.Reader
	io.Closer
}

func (s *solution) response(req *http.Request) *http.Response {
	headers := make(http.Header, len(s.Headers)+1)
	for key, value := range s.Headers {
		switch strings.ToLower(key) {
		case "content-encoding", "content-length", "transfer-encoding":
			continue
		default:
			headers.Set(key, value)
		}
	}
	for _, c := range s.Cookies {
		item := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		}
		if c.Expires > 0 {
			item.Expires = time.Unix(c.Expires, 0)
		}
		headers.Add("Set-Cookie", item.String())
	}

	body := s.Response
	resp := &http.Response{
		Status:        strconv.Itoa(s.Status) + " " + http.StatusText(s.Status),
		StatusCode:    s.Status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        headers,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
	return resp
}
