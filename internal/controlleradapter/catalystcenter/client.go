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
	c := &client{cfg: cfg}
	if cfg.MaxConcurrent > 0 {
		c.sem = make(chan struct{}, cfg.MaxConcurrent)
	}
	return c
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
	if c.rest != nil && c.rest.HTTPClient != nil {
		c.rest.HTTPClient.CloseIdleConnections()
	}
	c.rest, c.token, c.builtAt = nil, "", time.Time{}
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
	httpClient := &http.Client{Timeout: c.cfg.RequestTimeout, Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	return transport.NewRESTClient(c.cfg.Endpoint, transport.RESTClientOptions{HTTPClient: httpClient, RateLimiter: c.cfg.Limiter, Headers: map[string]string{"Accept": "application/json", "Content-Type": "application/json"}})
}

func (c *client) readCredentials() (string, string, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(c.cfg.CredentialPath, name))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
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
		return "", "", errors.New("Catalyst Center credentials must not be empty")
	}
	return u, p, nil
}

func (c *client) session(ctx context.Context) (*transport.RESTClient, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rest != nil && c.token != "" && time.Since(c.builtAt) < c.cfg.MaxSessionLifetime {
		return c.rest, c.token, nil
	}
	if c.rest != nil && c.rest.HTTPClient != nil {
		c.rest.HTTPClient.CloseIdleConnections()
	}
	rest, err := c.buildRESTLocked()
	if err != nil {
		return nil, "", err
	}
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
		return nil, "", errors.New("Catalyst Center authentication response contained no token")
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
	if transport.IsAuthRESTError(err) {
		c.Invalidate()
		rest, token, err = c.session(ctx)
		if err != nil {
			return nil, err
		}
		return rest.Do(ctx, transport.RESTRequest{Method: http.MethodGet, Path: path, Query: query, Headers: map[string]string{"X-Auth-Token": token}})
	}
	return body, err
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
	if transport.IsAuthRESTError(err) {
		return nil, err
	} // mutating requests are never replayed
	return resp, err
}

func (c *client) Probe(ctx context.Context) error {
	_, err := c.Get(ctx, devicesPath, url.Values{"limit": {"1"}})
	return err
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
	const pageSize = 500
	const maxPages = 100
	var out []Device
	seen := make(map[string]struct{})
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		offset := 1 + pageNumber*pageSize
		body, err := c.Get(ctx, devicesPath, url.Values{"limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}})
		if err != nil {
			return nil, err
		}
		var env apiEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("decode Catalyst Center devices: %w", err)
		}
		var page []Device
		if err := json.Unmarshal(env.Response, &page); err != nil {
			return nil, fmt.Errorf("decode Catalyst Center device list: %w", err)
		}
		for _, device := range page {
			if device.ID == "" {
				continue
			}
			if _, exists := seen[device.ID]; exists {
				return nil, errors.New("Catalyst Center returned a repeated device ID while paging")
			}
			seen[device.ID] = struct{}{}
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
	}
	return nil, errors.New("Catalyst Center device inventory exceeded the paging limit")
}

type Image struct {
	ID       string `json:"imageUuid"`
	FileName string `json:"fileName"`
	Version  string `json:"version"`
	Family   string `json:"family"`
	Golden   bool   `json:"goldenImage"`
	Status   string `json:"status"`
}

func (c *client) ListImages(ctx context.Context) ([]Image, error) {
	body, err := c.Get(ctx, imagesPath, nil)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode Catalyst Center images: %w", err)
	}
	var out []Image
	if err := json.Unmarshal(env.Response, &out); err != nil {
		return nil, fmt.Errorf("decode Catalyst Center image list: %w", err)
	}
	return out, nil
}

type Task struct {
	ID  string `json:"taskId"`
	URL string `json:"url"`
}

func (c *client) Distribute(ctx context.Context, deviceID, imageID string) (Task, error) {
	return c.submit(ctx, distributePath, map[string]string{"deviceUuid": deviceID, "imageUuid": imageID})
}
func (c *client) Activate(ctx context.Context, deviceID, imageID string) (Task, error) {
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
		return Task{}, err
	}
	var task Task
	if err := json.Unmarshal(env.Response, &task); err != nil {
		return Task{}, err
	}
	if task.ID == "" {
		return Task{}, errors.New("Catalyst Center SWIM response contained no task ID")
	}
	return task, nil
}
func (c *client) GetTask(ctx context.Context, id string) ([]byte, error) {
	if id == "" || strings.ContainsAny(id, "/?#") {
		return nil, errors.New("invalid Catalyst Center task ID")
	}
	return c.Get(ctx, taskPath+id, nil)
}

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(transport.RedactCredentials(err.Error()))
}
