// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RateLimiter is the small interface implemented by golang.org/x/time/rate
// limiters and similar token-bucket wrappers.
type RateLimiter interface {
	Wait(context.Context) error
}

// RESTAuth carries basic-auth credentials for REST-style controller and device
// adapters. More complex auth flows stay in platform adapters and can pass
// their resulting token or cookie through RESTRequest.Headers.
type RESTAuth struct {
	Username string
	Password string
}

// RESTClientOptions configures a neutral REST helper used by protocol adapters.
type RESTClientOptions struct {
	HTTPClient  *http.Client
	Timeout     time.Duration
	TLSConfig   *tls.Config
	Auth        RESTAuth
	Headers     map[string]string
	RateLimiter RateLimiter
}

// RESTRequest is one HTTP request against RESTClient.BaseURL.
type RESTRequest struct {
	Method  string
	Path    string
	Query   url.Values
	Body    []byte
	Headers map[string]string
}

// RESTResponse is the raw response body and selected metadata. Callers that
// need cookies can use the HTTPClient's Jar or fall back to DoRaw.
type RESTResponse struct {
	StatusCode int
	Status     string
	Header     http.Header
	Body       []byte
}

// RESTClient provides protocol-neutral REST request mechanics for platform
// adapters. It intentionally does not own platform workflows such as DME login,
// Catalyst Center task polling, or FMC domain selection.
type RESTClient struct {
	BaseURL     *url.URL
	HTTPClient  *http.Client
	Auth        RESTAuth
	Headers     map[string]string
	RateLimiter RateLimiter
}

// NewRESTClient constructs a neutral REST client with secure defaults.
func NewRESTClient(baseURL string, opts RESTClientOptions) (*RESTClient, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse REST base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("REST base URL must include scheme and host")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		httpClient = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: opts.TLSConfig,
			},
		}
	}
	headers := make(map[string]string, len(opts.Headers))
	for k, v := range opts.Headers {
		headers[k] = v
	}
	return &RESTClient{
		BaseURL:     u,
		HTTPClient:  httpClient,
		Auth:        opts.Auth,
		Headers:     headers,
		RateLimiter: opts.RateLimiter,
	}, nil
}

// URL builds an absolute URL from a slash-relative or absolute-path request
// path while preserving the base URL's prefix path.
func (c *RESTClient) URL(path string, query url.Values) (string, error) {
	if c == nil || c.BaseURL == nil {
		return "", fmt.Errorf("nil REST client")
	}
	out := *c.BaseURL
	basePath := strings.TrimRight(out.Path, "/")
	reqPath := strings.TrimSpace(path)
	if reqPath == "" {
		out.Path = basePath
	} else {
		out.Path = basePath + "/" + strings.TrimLeft(reqPath, "/")
	}
	out.RawQuery = query.Encode()
	return out.String(), nil
}

// Do sends req and returns the response body for 2xx statuses.
func (c *RESTClient) Do(ctx context.Context, req RESTRequest) ([]byte, error) {
	resp, err := c.DoRaw(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// DoRaw sends req and returns body plus response metadata for 2xx statuses.
func (c *RESTClient) DoRaw(ctx context.Context, req RESTRequest) (*RESTResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("nil REST client")
	}
	if c.RateLimiter != nil {
		if err := c.RateLimiter.Wait(ctx); err != nil {
			return nil, err
		}
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	rawURL, err := c.URL(req.Path, req.Query)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if c.Auth.Username != "" || c.Auth.Password != "" {
		httpReq.SetBasicAuth(c.Auth.Username, c.Auth.Password)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	data, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		return nil, readErr
	}
	resp := &RESTResponse{
		StatusCode: httpResp.StatusCode,
		Status:     httpResp.Status,
		Header:     httpResp.Header.Clone(),
		Body:       data,
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		if len(data) > 0 {
			return resp, fmt.Errorf("REST %s %s failed with status %s: %s",
				method, req.Path, httpResp.Status, RedactCredentials(string(data)))
		}
		return resp, fmt.Errorf("REST %s %s failed with status %s", method, req.Path, httpResp.Status)
	}
	return resp, nil
}
