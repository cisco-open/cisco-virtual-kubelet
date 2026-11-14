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

package ap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
)

const (
	ioxAPIPrefix       = "/iox/api/v2/hosting"
	maxAPIResponseSize = 8 << 20
	maxPackageSize     = 512 << 20
)

type ioxClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

type appInfo struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

func newIOxClient(spec *v1alpha1.DeviceSpec) (*ioxClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("AP IOx client: nil device spec")
	}
	if strings.TrimSpace(spec.Address) == "" {
		return nil, fmt.Errorf("AP IOx client: device address is required")
	}
	port := spec.Port
	if port == 0 {
		port = 8443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("AP IOx client: invalid port")
	}
	host := spec.Address
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if spec.TLS != nil {
		configured, err := tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
		if err != nil {
			return nil, fmt.Errorf("AP IOx client: TLS configuration: %w", err)
		}
		tlsConfig = configured
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	client := &http.Client{Timeout: 10 * time.Minute, Transport: transport}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &ioxClient{
		baseURL:  "https://" + host + ":" + strconv.Itoa(port) + ioxAPIPrefix,
		username: spec.Username,
		password: spec.Password,
		http:     client,
	}, nil
}

func (c *ioxClient) do(ctx context.Context, method, endpoint, contentType string, body io.Reader, out any) error {
	if strings.HasPrefix(endpoint, "/") || strings.Contains(endpoint, "..") {
		return fmt.Errorf("AP IOx API: invalid endpoint")
	}
	requestURL := c.baseURL + "/" + endpoint
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("AP IOx API: create request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("AP IOx API request failed: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxAPIResponseSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("AP IOx API: read response: %w", err)
	}
	if len(data) > maxAPIResponseSize {
		return fmt.Errorf("AP IOx API: response exceeded size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ioxHTTPError{status: resp.StatusCode}
	}
	if out != nil && len(bytes.TrimSpace(data)) != 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("AP IOx API: invalid JSON response")
		}
	}
	return nil
}

type ioxHTTPError struct{ status int }

func (e *ioxHTTPError) Error() string { return fmt.Sprintf("AP IOx API returned HTTP %d", e.status) }

func (c *ioxClient) listApps(ctx context.Context) ([]appInfo, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "apps", "", nil, &raw); err != nil {
		return nil, err
	}
	var direct []appInfo
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	var wrapped struct {
		Apps  []appInfo `json:"apps"`
		Items []appInfo `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("AP IOx API: unrecognized application list response")
	}
	if wrapped.Apps != nil {
		return wrapped.Apps, nil
	}
	return wrapped.Items, nil
}

func (c *ioxClient) app(ctx context.Context, id string) (appInfo, error) {
	if err := validateAppID(id); err != nil {
		return appInfo{}, err
	}
	var app appInfo
	if err := c.do(ctx, http.MethodGet, "apps/"+url.PathEscape(id), "", nil, &app); err != nil {
		return appInfo{}, err
	}
	if app.ID != "" && app.ID != id {
		return appInfo{}, fmt.Errorf("AP IOx API returned a mismatched application ID")
	}
	app.ID = id
	return app, nil
}

func (c *ioxClient) install(ctx context.Context, id, filename string, packageData []byte) error {
	if err := validateAppID(id); err != nil {
		return err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", path.Base(filename))
	if err != nil {
		return fmt.Errorf("AP IOx API: create package upload: %w", err)
	}
	if _, err := part.Write(packageData); err != nil {
		return fmt.Errorf("AP IOx API: write package upload: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("AP IOx API: finish package upload: %w", err)
	}
	return c.do(ctx, http.MethodPost, "apps/"+url.PathEscape(id), w.FormDataContentType(), &body, nil)
}

func (c *ioxClient) action(ctx context.Context, id, action string, payload any) error {
	if err := validateAppID(id); err != nil {
		return err
	}
	switch action {
	case "activate", "start", "stop", "deactivate":
	default:
		return fmt.Errorf("AP IOx API: unsupported application action")
	}
	var body io.Reader
	contentType := ""
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("AP IOx API: encode activation payload: %w", err)
		}
		body = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	endpoint := "apps/" + url.PathEscape(id) + "/state?action=" + url.QueryEscape(action)
	return c.do(ctx, http.MethodPost, endpoint, contentType, body, nil)
}

func (c *ioxClient) uninstall(ctx context.Context, id string) error {
	if err := validateAppID(id); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "apps/"+url.PathEscape(id), "", nil, nil)
}
