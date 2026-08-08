// Package client is a thin wrapper over the HIOK REST API, shared by every
// resource and data source in the provider.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to one HIOK deployment.
type Client struct {
	Endpoint string
	Token    string
	Regions  []string
	http     *http.Client
}

// New signs in when a password is supplied, otherwise uses the token as-is.
func New(endpoint, token, email, password string, regions []string) (*Client, error) {
	c := &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Token:    token,
		Regions:  regions,
		http:     &http.Client{Timeout: 5 * time.Minute},
	}

	if c.Token == "" {
		if email == "" || password == "" {
			return nil, fmt.Errorf("either token, or email and password, must be configured")
		}
		if err := c.login(email, password); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Client) login(email, password string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, c.Endpoint+"/api/OAuth/token", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sign-in request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sign-in failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed struct {
		Data struct {
			Token   string `json:"token"`
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("could not read the sign-in response: %w", err)
	}
	if parsed.Data.Token == "" {
		return fmt.Errorf("sign-in failed: %s", parsed.Data.Message)
	}
	c.Token = parsed.Data.Token
	return nil
}

// Do issues an authenticated request and decodes the JSON envelope into out.
func (c *Client) Do(method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, c.Endpoint+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
