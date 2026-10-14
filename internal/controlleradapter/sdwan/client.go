// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client keeps session material inside the endpoint worker. Error bodies and
// URLs are deliberately excluded from returned errors.
type Client struct {
	Changes                          <-chan struct{}
	dirty                            bool
	Guard                            func(context.Context) error
	nextRequest                      time.Time
	RequestsPerSecond                int32
	Endpoint, CredentialPath, CAPath string
	InsecureSkipVerify               bool
	Timeout, MaxSessionLifetime      time.Duration
	mu                               sync.Mutex
	http                             *http.Client
	token                            string
	created                          time.Time
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("SD-WAN API HTTP %d", e.Status) }

var ErrResponse = errors.New("invalid SD-WAN API response")
var errMaterialChanged = errors.New("SD-WAN credential or trust material changed")

func (c *Client) Invalidate() { c.mu.Lock(); defer c.mu.Unlock(); c.drop() }
func (c *Client) drop() {
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	c.http = nil
	c.token = ""
}
func (c *Client) connect(ctx context.Context) error {
	select {
	case <-c.Changes:
		c.dirty = true
	default:
	}
	if c.dirty {
		c.drop()
		c.dirty = false
	}
	if c.http != nil && time.Since(c.created) < c.MaxSessionLifetime {
		return nil
	}
	c.drop()
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid SD-WAN HTTPS endpoint")
	}
	user, err := os.ReadFile(filepath.Join(c.CredentialPath, "username"))
	if err != nil || len(bytes.TrimSpace(user)) == 0 {
		return errors.New("SD-WAN username projection unavailable")
	}
	pass, err := os.ReadFile(filepath.Join(c.CredentialPath, "password"))
	if err != nil || len(pass) == 0 {
		return errors.New("SD-WAN password projection unavailable")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureSkipVerify} // explicit endpoint lab opt-in
	if c.CAPath != "" {
		b, e := os.ReadFile(c.CAPath)
		if e != nil {
			return errors.New("SD-WAN CA projection unavailable")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(b) {
			return errors.New("invalid SD-WAN CA bundle")
		}
		tc.RootCAs = pool
	}
	jar, _ := cookiejar.New(nil)
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if c.MaxSessionLifetime <= 0 {
		c.MaxSessionLifetime = 15 * time.Minute
	}
	c.http = &http.Client{Timeout: timeout, Jar: jar, Transport: &http.Transport{TLSClientConfig: tc}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	payload := url.Values{"j_username": {strings.TrimSpace(string(user))}, "j_password": {string(pass)}}.Encode()
	body, err := c.raw(ctx, http.MethodPost, "/j_security_check", []byte(payload), "application/x-www-form-urlencoded")
	if err != nil {
		c.drop()
		return err
	}
	// A login failure may return HTML with HTTP 200; a cookie alone is insufficient.
	hasSession := false
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "JSESSIONID" && cookie.Value != "" {
			hasSession = true
		}
	}
	if !hasSession || len(bytes.TrimSpace(body)) != 0 {
		c.drop()
		return errors.New("SD-WAN authentication rejected")
	}
	token, err := c.raw(ctx, http.MethodGet, "/dataservice/client/token", nil, "")
	if err != nil {
		c.drop()
		return err
	}
	t := strings.TrimSpace(string(token))
	if t == "" || len(t) > 4096 || strings.ContainsAny(t, "<>\r\n \t") {
		c.drop()
		return ErrResponse
	}
	c.token = t
	c.created = time.Now()
	return nil
}
func (c *Client) raw(ctx context.Context, method, path string, body []byte, contentType string) ([]byte, error) {
	if c.Guard != nil {
		if err := c.Guard(ctx); err != nil {
			return nil, err
		}
	}
	rate := c.RequestsPerSecond
	if rate <= 0 {
		rate = 5
	}
	if wait := time.Until(c.nextRequest); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	c.nextRequest = time.Now().Add(time.Second / time.Duration(rate))
	select {
	case <-c.Changes:
		c.dirty = true
		return nil, errMaterialChanged
	default:
	}
	if c.Guard != nil {
		if err := c.Guard(ctx); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Endpoint, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrResponse
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("X-XSRF-TOKEN", c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("SD-WAN transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &HTTPError{res.StatusCode}
	}
	const maxBody = 16 << 20
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		return nil, ErrResponse
	}
	return b, nil
}

// Request serializes session refresh and requests. Only GET may retry after 401;
// a mutation with an uncertain outcome must be recovered by its durable journal.
func (c *Client) Request(ctx context.Context, method, path string, input, output any) error {
	hierarchyQuery := method == "GET" && regexp.MustCompile(`^/dataservice/v1/network-hierarchy\?offset=[0-9]+&limit=1500$`).MatchString(path)
	tagDeleteQuery := method == "DELETE" && regexp.MustCompile(`^/dataservice/v1/tags\?tagId=[A-Za-z0-9_-]{1,128}$`).MatchString(path)
	tagAssociateQuery := method == "POST" && path == "/dataservice/v1/tags/associate?operationType=DELETE"
	deviceTagQuery := method == "GET" && regexp.MustCompile(`^/dataservice/system/device/vedges\?uuid=[A-Za-z0-9_-]{1,128}$`).MatchString(path)
	if !strings.HasPrefix(path, "/dataservice/") || (!(hierarchyQuery || tagDeleteQuery || tagAssociateQuery || deviceTagQuery) && strings.ContainsAny(path, "?#")) || strings.Contains(path, "..") {
		return errors.New("invalid SD-WAN API path")
	}
	var b []byte
	var err error
	if input != nil {
		b, err = json.Marshal(input)
		if err != nil {
			return ErrResponse
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = c.connect(ctx); err != nil {
		return err
	}
	// Manager 20.18 may take over 30 seconds to answer a custom-app POST,
	// even when it rejects the request. Keep the normal mutation timeout for
	// every other endpoint while allowing this observed asynchronous API time.
	if method == http.MethodPost && path == "/dataservice/template/policy/customapp/" && c.http.Timeout < 2*time.Minute {
		original := c.http.Timeout
		c.http.Timeout = 2 * time.Minute
		defer func() { c.http.Timeout = original }()
	}
	result, err := c.raw(ctx, method, path, b, "application/json")
	var he *HTTPError
	if method == http.MethodGet && errors.As(err, &he) && he.Status == 401 {
		c.drop()
		if err = c.connect(ctx); err != nil {
			return err
		}
		result, err = c.raw(ctx, method, path, b, "application/json")
	}
	if err != nil {
		return err
	}
	if output == nil {
		return nil
	}
	if json.Unmarshal(result, output) != nil {
		return ErrResponse
	}
	return nil
}

type Inventory struct {
	Count       int
	Version     string
	DeviceTypes map[string]int
}

func (c *Client) Discover(ctx context.Context) (Inventory, error) {
	var server struct {
		Data struct {
			Version string `json:"platformVersion"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", "/dataservice/client/server", nil, &server); err != nil {
		return Inventory{}, err
	}
	if server.Data.Version == "" {
		return Inventory{}, ErrResponse
	}
	var devices struct {
		Data *[]struct {
			Type string `json:"device-type"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", "/dataservice/device", nil, &devices); err != nil {
		return Inventory{}, err
	}
	if devices.Data == nil {
		return Inventory{}, ErrResponse
	}
	result := Inventory{Version: server.Data.Version, Count: len(*devices.Data), DeviceTypes: map[string]int{}}
	for _, d := range *devices.Data {
		if d.Type == "" {
			return Inventory{}, ErrResponse
		}
		result.DeviceTypes[d.Type]++
	}
	return result, nil
}
