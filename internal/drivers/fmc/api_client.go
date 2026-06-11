// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	defaultFMCAPIPort                  = 443
	defaultFMCAPIRequestTimeoutSeconds = 60
	defaultFMCDomainName               = "Global"
)

// DomainInfo is one FMC domain available to the authenticated user.
type DomainInfo struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// ServerVersion is the FMC server-version payload reduced to the fields CVK reports.
type ServerVersion struct {
	Hostname      string `json:"hostname"`
	ServerVersion string `json:"serverVersion"`
	Model         string `json:"model"`
	SerialNumber  string `json:"serialNumber"`
	UUID          string `json:"uuid"`
	VDBVersion    string `json:"vdbVersion"`
	SRUVersion    string `json:"sruVersion"`
	LSPVersion    string `json:"lspVersion"`
	Uptime        string `json:"uptime"`
	Platform      string `json:"platform"`
	Type          string `json:"type"`
}

// SmartLicense captures the FMC smart-license state used for node health messaging.
type SmartLicense struct {
	RegStatus string         `json:"regStatus"`
	Metadata  map[string]any `json:"metadata"`
	Type      string         `json:"type"`
}

// ManagedDevice captures one FMC-managed device record.
type ManagedDevice struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	Model            string `json:"model"`
	HostName         string `json:"hostName"`
	HealthStatus     string `json:"healthStatus"`
	HealthMessage    string `json:"healthMessage"`
	DeploymentStatus string `json:"deploymentStatus"`
	SoftwareVersion  string `json:"sw_version"`
	IsConnected      bool   `json:"isConnected"`
}

type fmcListResponse struct {
	Items  []map[string]any `json:"items"`
	Paging struct {
		Offset int      `json:"offset"`
		Limit  int      `json:"limit"`
		Count  int      `json:"count"`
		Pages  int      `json:"pages"`
		Next   []string `json:"next"`
	} `json:"paging"`
}

// APIClient is the small FMC REST surface used by the FMC driver and NetAsCode applier.
type APIClient struct {
	baseURL    *url.URL
	username   string
	password   string
	domainName string
	domainUUID string
	http       *http.Client

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	domains      map[string]string
}

func NewAPIClientFromSpec(spec *v1alpha1.DeviceSpec, password string) (*APIClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("fmc api: nil device spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("fmc api: device address is required")
	}
	if spec.Username == "" {
		return nil, fmt.Errorf("fmc api: username is required")
	}
	if password == "" {
		password = spec.Password
	}
	if password == "" {
		return nil, fmt.Errorf("fmc api: password is required")
	}
	base, err := fmcAPIBaseURL(spec)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: fmcAPIInsecureSkipVerify(spec)} // #nosec G402 - lab FMCv commonly uses a self-signed cert.
	domainName := defaultFMCDomainName
	if spec.FMC != nil && spec.FMC.API != nil && spec.FMC.API.DomainName != "" {
		domainName = spec.FMC.API.DomainName
	}
	domainUUID := ""
	if spec.FMC != nil && spec.FMC.API != nil {
		domainUUID = spec.FMC.API.DomainUUID
	}
	return &APIClient{
		baseURL:    base,
		username:   spec.Username,
		password:   password,
		domainName: domainName,
		domainUUID: domainUUID,
		http: &http.Client{
			Timeout:   fmcAPIRequestTimeout(spec),
			Transport: transport,
		},
		domains: map[string]string{},
	}, nil
}

func (c *APIClient) Close() error {
	if c == nil || c.http == nil {
		return nil
	}
	c.http.CloseIdleConnections()
	return nil
}

func (c *APIClient) BaseURL() string {
	if c == nil || c.baseURL == nil {
		return ""
	}
	return c.baseURL.String()
}

func (c *APIClient) Check(ctx context.Context) error {
	_, err := c.ServerVersion(ctx)
	return err
}

func (c *APIClient) ServerVersion(ctx context.Context) (*ServerVersion, error) {
	raw, err := c.Get(ctx, "/api/fmc_platform/v1/info/serverversion")
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []ServerVersion `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("fmc api parse server version: %w", err)
	}
	if len(body.Items) == 0 {
		return nil, fmt.Errorf("fmc api server version: no items returned")
	}
	return &body.Items[0], nil
}

func (c *APIClient) SmartLicense(ctx context.Context) (*SmartLicense, error) {
	raw, err := c.Get(ctx, "/api/fmc_platform/v1/license/smartlicenses")
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []SmartLicense `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("fmc api parse smart license: %w", err)
	}
	if len(body.Items) == 0 {
		return &SmartLicense{}, nil
	}
	return &body.Items[0], nil
}

func (c *APIClient) ManagedDevices(ctx context.Context) ([]ManagedDevice, error) {
	domainUUID, err := c.DomainUUIDForName(ctx, "")
	if err != nil {
		return nil, err
	}
	raw, err := c.Get(ctx, "/api/fmc_config/v1/domain/"+url.PathEscape(domainUUID)+"/devices/devicerecords?expanded=true&limit=1000")
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []ManagedDevice `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("fmc api parse managed devices: %w", err)
	}
	return body.Items, nil
}

func (c *APIClient) DomainUUIDForName(ctx context.Context, name string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("fmc api: nil client")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.authenticateLocked(ctx, false); err != nil {
		return "", err
	}
	if c.domainUUID != "" && (name == "" || strings.EqualFold(name, c.domainName)) {
		return c.domainUUID, nil
	}
	if strings.TrimSpace(name) == "" {
		name = c.domainName
	}
	if strings.TrimSpace(name) == "" {
		name = defaultFMCDomainName
	}
	for domainName, uuid := range c.domains {
		if strings.EqualFold(domainName, name) {
			return uuid, nil
		}
	}
	if c.domainUUID != "" && strings.EqualFold(name, defaultFMCDomainName) {
		return c.domainUUID, nil
	}
	return "", fmt.Errorf("fmc api: domain %q not available to authenticated user", name)
}

func (c *APIClient) Get(ctx context.Context, path string) ([]byte, error) {
	return c.request(ctx, http.MethodGet, path, nil, true)
}

func (c *APIClient) Post(ctx context.Context, path string, body any) ([]byte, error) {
	return c.request(ctx, http.MethodPost, path, body, true)
}

func (c *APIClient) Put(ctx context.Context, path string, body any) ([]byte, error) {
	return c.request(ctx, http.MethodPut, path, body, true)
}

func (c *APIClient) Delete(ctx context.Context, path string) ([]byte, error) {
	return c.request(ctx, http.MethodDelete, path, nil, true)
}

func (c *APIClient) ListItems(ctx context.Context, path string) ([]map[string]any, error) {
	path = withDefaultLimit(path, 1000)
	raw, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	var body fmcListResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("fmc api parse list %s: %w", path, err)
	}
	return body.Items, nil
}

func (c *APIClient) request(ctx context.Context, method, path string, body any, retryAuth bool) ([]byte, error) {
	if c == nil || c.baseURL == nil || c.http == nil {
		return nil, fmt.Errorf("fmc api: uninitialised client")
	}
	if !strings.Contains(path, "/auth/generatetoken") {
		c.mu.Lock()
		err := c.authenticateLocked(ctx, false)
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	u, err := c.resolveURL(path)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("fmc api marshal %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, err
	}
	if strings.Contains(path, "/auth/generatetoken") {
		req.SetBasicAuth(c.username, c.password)
	} else {
		c.mu.Lock()
		token := c.accessToken
		domainUUID := c.domainUUID
		c.mu.Unlock()
		req.Header.Set("X-auth-access-token", token)
		if domainUUID != "" {
			req.Header.Set("DOMAIN_UUID", domainUUID)
		}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fmc api %s %s: %w", method, u.Redacted(), err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, fmt.Errorf("fmc api %s %s read response: %w", method, u.Redacted(), readErr)
	}
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && retryAuth && !strings.Contains(path, "/auth/generatetoken") {
		c.mu.Lock()
		c.accessToken = ""
		c.refreshToken = ""
		err := c.authenticateLocked(ctx, true)
		c.mu.Unlock()
		if err != nil {
			return raw, err
		}
		return c.request(ctx, method, path, body, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, fmt.Errorf("fmc api %s %s: HTTP %d: %s", method, u.Redacted(), resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c *APIClient) authenticateLocked(ctx context.Context, force bool) error {
	if c.accessToken != "" && !force {
		return nil
	}
	if c == nil || c.http == nil {
		return fmt.Errorf("fmc api: uninitialised client")
	}
	u, err := c.resolveURL("/api/fmc_platform/v1/auth/generatetoken")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fmc api auth %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return fmt.Errorf("fmc api auth read response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fmc api auth %s: HTTP %d: %s", u.Redacted(), resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	access := strings.TrimSpace(resp.Header.Get("X-auth-access-token"))
	if access == "" {
		return fmt.Errorf("fmc api auth %s: missing X-auth-access-token", u.Redacted())
	}
	c.accessToken = access
	c.refreshToken = strings.TrimSpace(resp.Header.Get("X-auth-refresh-token"))
	if headerUUID := strings.TrimSpace(resp.Header.Get("DOMAIN_UUID")); c.domainUUID == "" && headerUUID != "" {
		c.domainUUID = headerUUID
	}
	if domainsHeader := strings.TrimSpace(resp.Header.Get("DOMAINS")); domainsHeader != "" {
		var domains []DomainInfo
		if err := json.Unmarshal([]byte(domainsHeader), &domains); err == nil {
			for _, domain := range domains {
				if domain.Name != "" && domain.UUID != "" {
					c.domains[domain.Name] = domain.UUID
				}
			}
		}
	}
	if c.domainName == "" {
		c.domainName = defaultFMCDomainName
	}
	if c.domainUUID == "" {
		if uuid := c.domains[c.domainName]; uuid != "" {
			c.domainUUID = uuid
		}
	}
	return nil
}

func (c *APIClient) resolveURL(path string) (*url.URL, error) {
	if parsed, err := url.Parse(path); err == nil && parsed.IsAbs() {
		return parsed, nil
	}
	if c.baseURL == nil {
		return nil, fmt.Errorf("fmc api: nil base URL")
	}
	if strings.Contains(path, "?") {
		parts := strings.SplitN(path, "?", 2)
		return c.baseURL.ResolveReference(&url.URL{Path: joinURLPath(c.baseURL.Path, parts[0]), RawQuery: parts[1]}), nil
	}
	return c.baseURL.ResolveReference(&url.URL{Path: joinURLPath(c.baseURL.Path, path)}), nil
}

func fmcAPIBaseURL(spec *v1alpha1.DeviceSpec) (*url.URL, error) {
	if spec != nil && spec.FMC != nil && spec.FMC.API != nil && spec.FMC.API.BaseURL != "" {
		u, err := url.Parse(spec.FMC.API.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("fmc api baseUrl: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("fmc api baseUrl must include scheme and host")
		}
		return u, nil
	}
	port := defaultFMCAPIPort
	if spec != nil && spec.FMC != nil && spec.FMC.API != nil && spec.FMC.API.Port > 0 {
		port = spec.FMC.API.Port
	} else if spec != nil && spec.Port > 0 {
		port = spec.Port
	}
	return url.Parse("https://" + joinHostPort(spec.Address, port))
}

func fmcAPIRequestTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultFMCAPIRequestTimeoutSeconds
	if spec != nil && spec.FMC != nil && spec.FMC.API != nil && spec.FMC.API.RequestTimeoutSeconds > 0 {
		seconds = spec.FMC.API.RequestTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func fmcAPIInsecureSkipVerify(spec *v1alpha1.DeviceSpec) bool {
	if spec != nil && spec.FMC != nil && spec.FMC.API != nil {
		return spec.FMC.API.InsecureSkipVerify
	}
	return true
}

func joinHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

func joinURLPath(base, path string) string {
	if path == "" {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func withDefaultLimit(path string, limit int) string {
	if strings.Contains(path, "limit=") {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "limit=" + strconv.Itoa(limit)
}
