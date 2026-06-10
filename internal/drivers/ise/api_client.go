// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

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
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	defaultISEAPIPort                  = 443
	defaultISEAPIRequestTimeoutSeconds = 60
)

// APIClient is the small REST/ERS surface the ISE NetAsCode applier needs.
type APIClient struct {
	baseURL  *url.URL
	username string
	password string
	http     *http.Client
}

func NewAPIClientFromSpec(spec *v1alpha1.DeviceSpec, password string) (*APIClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("ise api: nil device spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("ise api: device address is required")
	}
	if spec.Username == "" {
		return nil, fmt.Errorf("ise api: username is required")
	}
	if password == "" {
		password = spec.Password
	}
	if password == "" {
		return nil, fmt.Errorf("ise api: password is required")
	}
	base, err := iseAPIBaseURL(spec)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: iseAPIInsecureSkipVerify(spec)} // #nosec G402 - configurable for lab self-signed ISE certs.
	return &APIClient{
		baseURL:  base,
		username: spec.Username,
		password: password,
		http: &http.Client{
			Timeout:   iseAPIRequestTimeout(spec),
			Transport: transport,
		},
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
	if c == nil {
		return fmt.Errorf("ise api: nil client")
	}
	// /admin/login.jsp is present even when ERS is not enabled. It proves the
	// appliance HTTPS/API gateway is reachable; ERS failures are surfaced when a
	// resource write is attempted.
	_, err := c.request(ctx, http.MethodGet, "/admin/login.jsp", nil)
	return err
}

func (c *APIClient) Get(ctx context.Context, path string) ([]byte, error) {
	return c.request(ctx, http.MethodGet, path, nil)
}

func (c *APIClient) Post(ctx context.Context, path string, body any) ([]byte, error) {
	return c.request(ctx, http.MethodPost, path, body)
}

func (c *APIClient) Put(ctx context.Context, path string, body any) ([]byte, error) {
	return c.request(ctx, http.MethodPut, path, body)
}

func (c *APIClient) Delete(ctx context.Context, path string) ([]byte, error) {
	return c.request(ctx, http.MethodDelete, path, nil)
}

func (c *APIClient) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if c == nil || c.baseURL == nil || c.http == nil {
		return nil, fmt.Errorf("ise api: uninitialised client")
	}
	u := c.baseURL.ResolveReference(&url.URL{Path: joinURLPath(c.baseURL.Path, path)})
	if strings.Contains(path, "?") {
		parts := strings.SplitN(path, "?", 2)
		u = c.baseURL.ResolveReference(&url.URL{Path: joinURLPath(c.baseURL.Path, parts[0]), RawQuery: parts[1]})
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ise api marshal %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ise api %s %s: %w", method, u.Redacted(), err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return nil, fmt.Errorf("ise api %s %s read response: %w", method, u.Redacted(), readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, fmt.Errorf("ise api %s %s: HTTP %d: %s", method, u.Redacted(), resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func iseAPIBaseURL(spec *v1alpha1.DeviceSpec) (*url.URL, error) {
	if spec != nil && spec.ISE != nil && spec.ISE.API != nil && spec.ISE.API.BaseURL != "" {
		u, err := url.Parse(spec.ISE.API.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("ise api baseUrl: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("ise api baseUrl must include scheme and host")
		}
		return u, nil
	}
	port := defaultISEAPIPort
	if spec != nil && spec.ISE != nil && spec.ISE.API != nil && spec.ISE.API.Port > 0 {
		port = spec.ISE.API.Port
	} else if spec != nil && spec.Port > 0 {
		port = spec.Port
	}
	return url.Parse("https://" + joinHostPort(spec.Address, port))
}

func iseAPIRequestTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultISEAPIRequestTimeoutSeconds
	if spec != nil && spec.ISE != nil && spec.ISE.API != nil && spec.ISE.API.RequestTimeoutSeconds > 0 {
		seconds = spec.ISE.API.RequestTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func iseAPIInsecureSkipVerify(spec *v1alpha1.DeviceSpec) bool {
	if spec != nil && spec.ISE != nil && spec.ISE.API != nil {
		return spec.ISE.API.InsecureSkipVerify
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
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}
