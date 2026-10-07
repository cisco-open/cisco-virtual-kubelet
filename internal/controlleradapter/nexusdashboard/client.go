// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nexusdashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

const (
	loginPath  = "/api/v1/infra/login"
	probePath  = "/api/v1/manage/fabrics"
	authCookie = "AuthCookie"

	defaultDomain = "local"
	maxBodyBytes  = 512
)

var (
	errAuthentication = errors.New("authentication failed")
	errCredentials    = errors.New("invalid credential material")
)

// clientConfig is the validated, immutable input to a client.
type clientConfig struct {
	Endpoint           string
	CredentialPath     string
	CAPath             string
	InsecureSkipVerify bool
	RequestTimeout     time.Duration
	MaxSessionLifetime time.Duration
	MaxConcurrent      int
	Limiter            transport.RateLimiter
}

// client is the private Nexus Dashboard API client. It logs in with the
// mounted credential files, caches the resulting token, and re-logs in once on
// a 401 for read-only requests. Mutating requests are never replayed.
type client struct {
	cfg clientConfig
	sem chan struct{}

	mu      sync.Mutex
	rest    *transport.RESTClient
	token   string
	builtAt time.Time
}

func newClient(cfg clientConfig) *client {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	return &client{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent)}
}

// Invalidate drops the cached token and TLS state; the next request re-reads
// every mounted file.
func (c *client) Invalidate() {
	c.mu.Lock()
	c.dropLocked()
	c.mu.Unlock()
}

func (c *client) dropLocked() {
	if c.rest != nil && c.rest.HTTPClient != nil {
		c.rest.HTTPClient.CloseIdleConnections()
	}
	c.rest, c.token, c.builtAt = nil, "", time.Time{}
}

// Get performs an authenticated, retried GET and returns the response body.
func (c *client) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var out []byte
	err := transport.RetryIdempotent(ctx, transport.RetryPolicy{}, func() error {
		body, err := c.getOnce(ctx, path, query)
		out = body
		return err
	})
	return out, err
}

func (c *client) getOnce(ctx context.Context, path string, query url.Values) ([]byte, error) {
	body, err := c.authedGet(ctx, path, query)
	if transport.IsAuthRESTError(err) {
		c.Invalidate()
		body, err = c.authedGet(ctx, path, query)
	}
	return body, err
}

func (c *client) authedGet(ctx context.Context, path string, query url.Values) ([]byte, error) {
	rest, token, err := c.session(ctx)
	if err != nil {
		return nil, err
	}
	return rest.Do(ctx, transport.RESTRequest{
		Method:  http.MethodGet,
		Path:    path,
		Query:   query,
		Headers: map[string]string{"Cookie": authCookie + "=" + token},
	})
}

// session returns a live REST client and token, logging in when needed.
func (c *client) session(ctx context.Context) (*transport.RESTClient, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rest != nil && c.token != "" && time.Since(c.builtAt) < c.cfg.MaxSessionLifetime {
		return c.rest, c.token, nil
	}
	c.dropLocked()
	rest, err := c.buildRESTLocked()
	if err != nil {
		return nil, "", err
	}
	token, err := c.login(ctx, rest)
	if err != nil {
		return nil, "", err
	}
	c.rest, c.token, c.builtAt = rest, token, time.Now()
	return rest, token, nil
}

func (c *client) buildRESTLocked() (*transport.RESTClient, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.cfg.InsecureSkipVerify} //nolint:gosec // explicit lab opt-in
	if c.cfg.CAPath != "" {
		pem, err := os.ReadFile(c.cfg.CAPath)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA bundle contains no valid PEM certificates")
		}
		tlsCfg.RootCAs = pool
	}
	httpClient := &http.Client{
		Timeout:   c.cfg.RequestTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	return transport.NewRESTClient(c.cfg.Endpoint, transport.RESTClientOptions{
		HTTPClient:  httpClient,
		RateLimiter: c.cfg.Limiter,
		Headers:     map[string]string{"Content-Type": "application/json", "Accept": "application/json"},
	})
}

type credentials struct{ username, password, domain string }

func (c *client) readCredentials() (credentials, error) {
	read := func(name string, required bool) (string, error) {
		raw, err := os.ReadFile(filepath.Join(c.cfg.CredentialPath, name))
		if err != nil {
			if !required && errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			return "", fmt.Errorf("%w: read %q: %v", errCredentials, name, errors.Unwrap(err))
		}
		return strings.TrimRight(string(raw), "\r\n"), nil
	}
	var cr credentials
	var err error
	if cr.username, err = read("username", true); err != nil {
		return cr, err
	}
	if cr.password, err = read("password", true); err != nil {
		return cr, err
	}
	if cr.domain, err = read("domain", false); err != nil {
		return cr, err
	}
	if cr.username == "" || cr.password == "" {
		return cr, fmt.Errorf("%w: username and password must not be empty", errCredentials)
	}
	if cr.domain == "" {
		cr.domain = defaultDomain
	}
	return cr, nil
}

func (c *client) login(ctx context.Context, rest *transport.RESTClient) (string, error) {
	cr, err := c.readCredentials()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]string{
		"domain": cr.domain, "userName": cr.username, "userPasswd": cr.password,
	})
	if err != nil {
		return "", err
	}
	resp, err := rest.DoRaw(ctx, transport.RESTRequest{Method: http.MethodPost, Path: loginPath, Body: payload})
	if err != nil {
		if transport.IsAuthRESTError(err) {
			return "", errAuthentication
		}
		return "", sanitizeError(err)
	}
	var parsed struct {
		Token    string `json:"token"`
		JWTToken string `json:"jwttoken"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return "", errors.New("login response is not valid JSON")
	}
	token := parsed.Token
	if token == "" {
		token = parsed.JWTToken
	}
	if token == "" {
		for _, ck := range (&http.Response{Header: resp.Header}).Cookies() {
			if ck.Name == authCookie {
				token = ck.Value
			}
		}
	}
	if token == "" {
		return "", errors.New("login response contained no token")
	}
	return token, nil
}

// sanitizeError returns an error safe for status and logs: redacted and bounded.
func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	var restErr *transport.RESTError
	if errors.As(err, &restErr) {
		clone := *restErr
		if len(clone.Body) > maxBodyBytes {
			clone.Body = clone.Body[:maxBodyBytes]
		}
		return &clone
	}
	return errors.New(transport.RedactCredentials(err.Error()))
}

// Probe verifies authentication and Manage API compatibility.
func (c *client) Probe(ctx context.Context) error {
	_, err := c.Get(ctx, probePath, nil)
	return err
}
