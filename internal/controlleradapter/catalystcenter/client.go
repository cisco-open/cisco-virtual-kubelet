// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

const (
	authPath       = "/dna/system/api/v1/auth/token"
	devicesPath    = "/dna/intent/api/v1/network-device"
	imagesPath     = "/dna/intent/api/v1/image/importation"
	distributePath = "/dna/intent/api/v1/image/distribution"
	activatePath   = "/dna/intent/api/v1/image/activation/device"
	taskPath       = "/dna/intent/api/v1/task/"
)

var (
	errCredentials     = errors.New("invalid Catalyst Center credential material")
	errInvalidResponse = errors.New("invalid Catalyst Center API response")
)

type clientConfig struct {
	Endpoint           string
	CredentialPath     string
	CAPath             string
	InsecureSkipVerify bool
	RequestTimeout     time.Duration
	MaxSessionLifetime time.Duration
	Limiter            transport.RateLimiter
	MaxConcurrent      int
}

type client struct {
	cfg     clientConfig
	mu      sync.Mutex
	rest    *transport.RESTClient
	token   string
	builtAt time.Time
	sem     chan struct{}
}

func newClient(cfg clientConfig) *client {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 4
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxSessionLifetime <= 0 {
		cfg.MaxSessionLifetime = 15 * time.Minute
	}
	return &client{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent)}
}

func (c *client) acquire(ctx context.Context) (func(), error) {
	if c.sem == nil {
		return func() {}, nil
	}
	select {
	case c.sem <- struct{}{}:
		return func() { <-c.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *client) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropLocked()
}

func (c *client) dropLocked() {
	if c.rest != nil && c.rest.HTTPClient != nil {
		c.rest.HTTPClient.CloseIdleConnections()
	}
	c.rest, c.token, c.builtAt = nil, "", time.Time{}
}

// A late 401 from an old session must not discard another caller's new login.
func (c *client) invalidateSession(rest *transport.RESTClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rest == rest {
		c.dropLocked()
	}
}

func (c *client) buildRESTLocked() (*transport.RESTClient, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.cfg.InsecureSkipVerify} //nolint:gosec // explicit lab opt-in
	if c.cfg.CAPath != "" {
		pem, err := os.ReadFile(c.cfg.CAPath)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA bundle contains no valid PEM certificates")
		}
		tlsConfig.RootCAs = pool
	}
	httpClient := &http.Client{
		Timeout:   c.cfg.RequestTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		// X-Auth-Token is not a standard sensitive header in net/http's
		// redirect policy. Never forward it, or replay a POST, on redirects.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return transport.NewRESTClient(c.cfg.Endpoint, transport.RESTClientOptions{HTTPClient: httpClient, RateLimiter: c.cfg.Limiter, Headers: map[string]string{"Accept": "application/json", "Content-Type": "application/json"}})
}

func (c *client) readCredentials() (string, string, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(c.cfg.CredentialPath, name))
		if err != nil {
			return "", fmt.Errorf("%w: cannot read %s", errCredentials, name)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	u, err := read("username")
	if err != nil {
		return "", "", err
	}
	p, err := read("password")
	if err != nil {
		return "", "", err
	}
	if u == "" || p == "" {
		return "", "", errCredentials
	}
	return u, p, nil
}

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
	// Failed logins must also release idle connections.
	defer func() {
		if c.rest != rest {
			rest.HTTPClient.CloseIdleConnections()
		}
	}()
	u, p, err := c.readCredentials()
	if err != nil {
		return nil, "", err
	}
	resp, err := rest.DoRaw(ctx, transport.RESTRequest{Method: http.MethodPost, Path: authPath, Body: []byte("{}"), Headers: map[string]string{"Authorization": "Basic " + basicAuth(u, p)}})
	if err != nil {
		return nil, "", sanitizeError(err)
	}
	var body struct {
		Token string `json:"Token"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.Token == "" {
		return nil, "", fmt.Errorf("%w: authentication response contained no token", errInvalidResponse)
	}
	c.rest, c.token, c.builtAt = rest, body.Token, time.Now()
	return rest, body.Token, nil
}

func basicAuth(username, password string) string { return basicAuthEncode(username + ":" + password) }

func basicAuthEncode(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func (c *client) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	rest, token, err := c.session(ctx)
	if err != nil {
		return nil, err
	}
	body, err := rest.Do(ctx, transport.RESTRequest{Method: http.MethodGet, Path: path, Query: query, Headers: map[string]string{"X-Auth-Token": token}})
	var restErr *transport.RESTError
	if errors.As(err, &restErr) && restErr.StatusCode == http.StatusUnauthorized {
		c.invalidateSession(rest)
		rest, token, err = c.session(ctx)
		if err != nil {
			return nil, err
		}
		body, err = rest.Do(ctx, transport.RESTRequest{Method: http.MethodGet, Path: path, Query: query, Headers: map[string]string{"X-Auth-Token": token}})
	}
	return body, sanitizeError(err)
}

func (c *client) Post(ctx context.Context, path string, body []byte) ([]byte, error) {
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	rest, token, err := c.session(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := rest.Do(ctx, transport.RESTRequest{Method: http.MethodPost, Path: path, Body: body, Headers: map[string]string{"X-Auth-Token": token}})
	// Mutating requests are never replayed, including authentication errors.
	return resp, sanitizeError(err)
}

func (c *client) Probe(ctx context.Context) error {
	body, err := c.Get(ctx, devicesPath, url.Values{"limit": {"1"}, "offset": {"1"}})
	if err != nil {
		return err
	}
	devices, err := decodeList[Device](body)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if device.ID == "" {
			return errInvalidResponse
		}
	}
	return nil
}

type apiEnvelope struct {
	Response json.RawMessage `json:"response"`
}

type Device struct {
	ID              string `json:"id"`
	Hostname        string `json:"hostname"`
	ManagementIP    string `json:"managementIpAddress"`
	Serial          string `json:"serialNumber"`
	PlatformID      string `json:"platformId"`
	SoftwareVersion string `json:"softwareVersion"`
	Reachability    string `json:"reachabilityStatus"`
}

func (c *client) ListDevices(ctx context.Context) ([]Device, error) {
	return listInventory(ctx, c, devicesPath, func(d Device) string { return d.ID })
}

// These endpoints use one-based offsets. A malformed or repeating page is
// an error, never a successful partial inventory.
func listInventory[T any](ctx context.Context, c *client, path string, identity func(T) string) ([]T, error) {
	const pageSize = 500
	const maxPages = 100
	var out []T
	seen := make(map[string]struct{})
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		offset := 1 + pageNumber*pageSize
		body, err := c.Get(ctx, path, url.Values{"limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}})
		if err != nil {
			return nil, err
		}
		page, err := decodeList[T](body)
		if err != nil {
			return nil, err
		}
		if len(page) > pageSize {
			return nil, fmt.Errorf("%w: inventory page exceeds requested limit", errInvalidResponse)
		}
		for _, item := range page {
			id := identity(item)
			if id == "" {
				return nil, fmt.Errorf("%w: inventory entry has no identity", errInvalidResponse)
			}
			if _, exists := seen[id]; exists {
				return nil, fmt.Errorf("%w: repeated inventory identity while paging", errInvalidResponse)
			}
			seen[id] = struct{}{}
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
	}
	return nil, errors.New("Catalyst Center inventory exceeded the paging limit")
}

func decodeList[T any](body []byte) ([]T, error) {
	var env apiEnvelope
	if json.Unmarshal(body, &env) != nil {
		return nil, errInvalidResponse
	}
	var out []T
	if json.Unmarshal(env.Response, &out) != nil || out == nil {
		return nil, errInvalidResponse
	}
	return out, nil
}

type Image struct {
	ID              string `json:"imageUuid"`
	Name            string `json:"name"`
	ImageName       string `json:"imageName"`
	Version         string `json:"version"`
	Family          string `json:"family"`
	Golden          bool   `json:"isTaggedGolden"`
	IntegrityStatus string `json:"imageIntegrityStatus"`
}

func (c *client) ListImages(ctx context.Context) ([]Image, error) {
	return listInventory(ctx, c, imagesPath, func(i Image) string { return i.ID })
}

type Task struct {
	ID  string `json:"taskId"`
	URL string `json:"url"`
}

func (c *client) Distribute(ctx context.Context, deviceID, imageID string) (Task, error) {
	if !validAPIID(deviceID) || !validAPIID(imageID) {
		return Task{}, errors.New("invalid SWIM device or image ID")
	}
	return c.submit(ctx, distributePath, map[string]string{"deviceUuid": deviceID, "imageUuid": imageID})
}
func (c *client) Activate(ctx context.Context, deviceID, imageID string) (Task, error) {
	if !validAPIID(deviceID) || !validAPIID(imageID) {
		return Task{}, errors.New("invalid SWIM device or image ID")
	}
	return c.submit(ctx, activatePath, map[string]any{"deviceUuid": deviceID, "imageUuidList": []string{imageID}})
}
func (c *client) submit(ctx context.Context, path string, payload any) (Task, error) {
	body, err := json.Marshal([]any{payload})
	if err != nil {
		return Task{}, err
	}
	raw, err := c.Post(ctx, path, body)
	if err != nil {
		return Task{}, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Task{}, errInvalidResponse
	}
	var task Task
	if err := json.Unmarshal(env.Response, &task); err != nil {
		return Task{}, errInvalidResponse
	}
	if !validAPIID(task.ID) {
		return Task{}, fmt.Errorf("%w: SWIM response contained no valid task ID", errInvalidResponse)
	}
	return task, nil
}
func (c *client) GetTask(ctx context.Context, id string) ([]byte, error) {
	if !validAPIID(id) {
		return nil, errors.New("invalid Catalyst Center task ID")
	}
	return c.Get(ctx, taskPath+id, nil)
}

func validAPIID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	var restErr *transport.RESTError
	if errors.As(err, &restErr) {
		clone := *restErr
		// Controller error bodies may echo credentials or tokens without a
		// recognizable key. Preserve classification, not remote content.
		clone.Body = ""
		clone.Status = http.StatusText(clone.StatusCode)
		return &clone
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("Catalyst Center request failed")
}
