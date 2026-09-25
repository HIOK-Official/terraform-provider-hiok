// Package client is a thin wrapper over the HIOK REST API, shared by every
// resource and data source in the provider.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client talks to one HIOK deployment.
type Client struct {
	Endpoint string
	Regions  []string

	mu       sync.Mutex
	token    string
	email    string
	password string
	http     *http.Client

	// RetryWait is the base delay between retries of transient failures.
	RetryWait time.Duration

	regionsOnce sync.Once
	regions     []Region
	regionsErr  error
}

// Region is one entry of GET /api/storageaccount/regions.
type Region struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Location    string `json:"location"`
	IsAvailable bool   `json:"isAvailable"`
}

// ListRegions returns the deployment's regions, fetched once per client.
func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	c.regionsOnce.Do(func() {
		var resp struct {
			Data []Region `json:"data"`
		}
		c.regionsErr = c.Do(ctx, http.MethodGet, "/api/storageaccount/regions", nil, &resp)
		c.regions = resp.Data
	})
	return c.regions, c.regionsErr
}

// CheckRegion fails fast for an unknown or unavailable region. The API does
// not reject those quickly: a VM create for an unknown region hangs until the
// edge proxy times out (HTTP 524) after about 100 seconds.
func (c *Client) CheckRegion(ctx context.Context, id string) error {
	regions, err := c.ListRegions(ctx)
	if err != nil || len(regions) == 0 {
		return nil // cannot check; let the API decide
	}
	var valid []string
	for _, r := range regions {
		if r.ID == id {
			if !r.IsAvailable {
				return fmt.Errorf("region %q exists but is not available for new resources right now", id)
			}
			return nil
		}
		if r.IsAvailable {
			valid = append(valid, r.ID)
		}
	}
	return fmt.Errorf("unknown region %q; available regions: %s", id, strings.Join(valid, ", "))
}

// DefaultRegion is the first available region reported by the API.
func (c *Client) DefaultRegion(ctx context.Context) (string, error) {
	regions, err := c.ListRegions(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range regions {
		if r.IsAvailable {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("the HIOK API reports no available region; set `regions` in the provider block")
}

// APIError is returned for any non-2xx response, or a 2xx response whose
// envelope reports success=false.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.StatusCode >= 200 && e.StatusCode <= 299 {
		return fmt.Sprintf("HIOK API rejected %s %s: %s", e.Method, e.Path, e.Message)
	}
	return fmt.Sprintf("%s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// DefaultRetryWait is the base delay between retries. Tests shrink it.
var DefaultRetryWait = 2 * time.Second

// IsTransient reports whether err is a temporary failure worth waiting out:
// a network error or a 429/502/503/504/520-524 response.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return transientStatus(apiErr.StatusCode)
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// New signs in when no token is supplied, otherwise uses the token as-is.
func New(endpoint, token, email, password string, regions []string) (*Client, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint must be configured (or set HIOK_ENDPOINT), e.g. https://hiokcloud.com")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q must be an absolute http(s) URL", endpoint)
	}

	c := &Client{
		Endpoint:  endpoint,
		Regions:   regions,
		token:     token,
		email:     email,
		password:  password,
		http:      &http.Client{Timeout: 5 * time.Minute},
		RetryWait: DefaultRetryWait,
	}

	if c.token == "" {
		if email == "" || password == "" {
			return nil, fmt.Errorf("either token, or email and password, must be configured")
		}
		if err := c.login(context.Background()); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) canRelogin() bool {
	return c.email != "" && c.password != ""
}

// login signs in, retrying for about two minutes while the API is
// temporarily unavailable (e.g. restarting behind its proxy).
func (c *Client) login(ctx context.Context) error {
	var err error
	for attempt := 1; attempt <= 8; attempt++ {
		var status int
		if status, err = c.loginOnce(ctx); err == nil || !(status == 0 || transientStatus(status)) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(c.RetryWait * time.Duration(attempt)):
		}
	}
	return err
}

func (c *Client) loginOnce(ctx context.Context) (int, error) {
	body, _ := json.Marshal(map[string]string{"email": c.email, "password": c.password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/api/OAuth/token", bytes.NewReader(body))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("sign-in request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("sign-in failed (%d): %s", resp.StatusCode, messageFrom(raw))
	}

	var parsed struct {
		Token string `json:"token"`
		Data  struct {
			Token   string `json:"token"`
			Success *bool  `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return resp.StatusCode, fmt.Errorf("could not read the sign-in response (is the endpoint correct?): %w", err)
	}
	token := parsed.Data.Token
	if token == "" {
		token = parsed.Token
	}
	if token == "" {
		msg := parsed.Data.Message
		if msg == "" {
			msg = parsed.Message
		}
		if msg == "" {
			msg = "no token in response"
		}
		return resp.StatusCode, fmt.Errorf("sign-in failed: %s", msg)
	}

	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	return resp.StatusCode, nil
}

// Do issues an authenticated request and decodes the JSON response into out.
//
// Transient failures (network errors, 429, 502-504) are retried for requests
// that are safe to repeat (GET, DELETE, and list-style POSTs flagged via
// idempotent). An expired token is refreshed once when email/password are set.
func (c *Client) Do(ctx context.Context, method, path string, in any, out any) error {
	return c.do(ctx, method, path, in, out, method == http.MethodGet || method == http.MethodDelete)
}

// DoIdempotent is Do for POST endpoints that only read data (e.g. list calls).
func (c *Client) DoIdempotent(ctx context.Context, method, path string, in any, out any) error {
	return c.do(ctx, method, path, in, out, true)
}

func (c *Client) do(ctx context.Context, method, path string, in any, out any, retryable bool) error {
	var buf []byte
	if in != nil {
		var err error
		if buf, err = json.Marshal(in); err != nil {
			return err
		}
	}

	const maxAttempts = 4
	relogged := false
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, raw, err := c.send(ctx, method, path, buf)
		switch {
		case err != nil:
			lastErr = fmt.Errorf("%s %s: %w", method, path, err)
			if ctx.Err() != nil || !retryable {
				return lastErr
			}
		case status == http.StatusUnauthorized && !relogged && c.canRelogin():
			relogged = true
			if err := c.login(ctx); err != nil {
				return fmt.Errorf("token expired and re-authentication failed: %w", err)
			}
			attempt-- // the re-login does not count as a retry
			continue
		case status < 200 || status > 299:
			lastErr = &APIError{Method: method, Path: path, StatusCode: status, Message: messageFrom(raw)}
			if !retryable || !transientStatus(status) {
				return lastErr
			}
		default:
			if ok, msg := envelopeFailed(raw); !ok {
				return &APIError{Method: method, Path: path, StatusCode: status, Message: msg}
			}
			if out == nil || len(bytes.TrimSpace(raw)) == 0 {
				return nil
			}
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("%s %s: unexpected response body: %w", method, path, err)
			}
			return nil
		}

		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(c.RetryWait * time.Duration(attempt)):
			}
		}
	}
	return lastErr
}

func (c *Client) send(ctx context.Context, method, path string, buf []byte) (int, []byte, error) {
	var body io.Reader
	if buf != nil {
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.currentToken())
	req.Header.Set("Accept", "application/json")
	if buf != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, raw, nil
}

func transientStatus(code int) bool {
	// 520-524 are Cloudflare's "origin unreachable / timed out" codes.
	return code == http.StatusTooManyRequests || code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout ||
		(code >= 520 && code <= 524)
}

// envelopeFailed inspects the HIOK response envelope. Some endpoints answer
// 2xx with {"success": false} or {"data": {"isSuccess": false, "data": "why"}}
// instead of an error status; treat that as a failure so the real message
// reaches the user.
func envelopeFailed(raw []byte) (ok bool, message string) {
	var env struct {
		Success   *bool           `json:"success"`
		IsSuccess *bool           `json:"isSuccess"`
		Message   string          `json:"message"`
		Error     string          `json:"error"`
		Errors    json.RawMessage `json:"errors"`
		Data      json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return true, ""
	}
	if (env.Success != nil && !*env.Success) || (env.IsSuccess != nil && !*env.IsSuccess) {
		return false, joinMessages(env.Message, env.Error, string(env.Errors))
	}
	var inner struct {
		Success   *bool           `json:"success"`
		IsSuccess *bool           `json:"isSuccess"`
		Message   string          `json:"message"`
		Data      json.RawMessage `json:"data"`
	}
	if len(env.Data) > 0 && env.Data[0] == '{' && json.Unmarshal(env.Data, &inner) == nil {
		if (inner.Success != nil && !*inner.Success) || (inner.IsSuccess != nil && !*inner.IsSuccess) {
			return false, joinMessages(env.Message, inner.Message, rawString(inner.Data))
		}
	}
	return true, ""
}

// messageFrom pulls a human-readable message out of an error body, e.g.
// {"message":"Failed to create storage account","error":"Invalid storage tier"},
// {"message":"Failed Deploying Virtual Machine.","data":{"data":"Failed to download VM image"}},
// or an ASP.NET validation problem {"title":...,"errors":{"id":["..."]}}.
func messageFrom(raw []byte) string {
	var env struct {
		Message string                     `json:"message"`
		Title   string                     `json:"title"`
		Error   string                     `json:"error"`
		Errors  map[string]json.RawMessage `json:"errors"`
		Data    json.RawMessage            `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil {
		var inner struct {
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		innerMsg := ""
		if len(env.Data) > 0 && env.Data[0] == '{' && json.Unmarshal(env.Data, &inner) == nil {
			innerMsg = joinMessages(inner.Message, rawString(inner.Data))
		}
		var validation []string
		for field, v := range env.Errors {
			var msgs []string
			if json.Unmarshal(v, &msgs) == nil {
				validation = append(validation, field+": "+strings.Join(msgs, "; "))
			}
		}
		if m := joinMessages(env.Message, env.Title, env.Error, innerMsg, strings.Join(validation, ", ")); m != "" {
			return m
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	if s == "" {
		s = "(empty response body)"
	}
	return s
}

// rawString returns v if it is a JSON string, else "".
func rawString(v json.RawMessage) string {
	var s string
	if len(v) > 0 && v[0] == '"' && json.Unmarshal(v, &s) == nil {
		return s
	}
	return ""
}

// joinMessages joins the distinct non-empty parts with ": ".
func joinMessages(parts ...string) string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "null" {
			continue
		}
		dup := false
		for _, o := range out {
			if o == p {
				dup = true
			}
		}
		if !dup {
			out = append(out, p)
		}
	}
	return strings.Join(out, ": ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Query builds an escaped query string, e.g. Query("vmName", "a&b") -> "?vmName=a%26b".
func Query(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return "?" + v.Encode()
}

// VisibleName strips the owner scope HIOK prefixes to stored names
// ("<owner>#<name>") and the leading slash Docker adds to container names.
func VisibleName(name string) string {
	if i := strings.LastIndex(name, "#"); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimPrefix(name, "/")
}
