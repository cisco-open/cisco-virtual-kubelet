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

package nxos

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
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
)

const (
	nxapiVersion = "1.0"
	nxapiPath    = "/ins"
)

type nxapiClient struct {
	rootURL     string
	baseURL     string
	username    string
	password    string
	client      *http.Client
	sessionLock *sync.Mutex
	mu          sync.Mutex
	dmeCookies  []*http.Cookie
}

func newNXAPIClient(spec *v1alpha1.DeviceSpec) (*nxapiClient, error) {
	return newNXAPIClientWithOptions(spec, nxapiClientOptions{})
}

type nxapiClientOptions struct {
	HTTPClient     *http.Client
	SessionLock    *sync.Mutex
	DefaultTimeout time.Duration
}

func newNXAPIClientWithOptions(spec *v1alpha1.DeviceSpec, opts nxapiClientOptions) (*nxapiClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("nxos nxapi: nil device spec")
	}
	scheme := "http"
	defaultPort := 80
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if spec.TLS != nil && spec.TLS.Enabled {
		// Shared device-client helper: TLS 1.2 minimum, InsecureSkipVerify
		// copied verbatim (operator-controlled for lab devices), spec.tls.caFile
		// loaded into RootCAs so private-CA Nexus front panels verify, and the
		// certFile/keyFile client pair when both are set.
		var err error
		tlsConfig, err = tlsutil.ClientTLSFromDeviceTLS(spec.TLS) // #nosec G402 - InsecureSkipVerify is operator-controlled.
		if err != nil {
			return nil, fmt.Errorf("nxos nxapi: TLS from spec: %w", err)
		}
		scheme = "https"
		defaultPort = 443
	}
	port := spec.Port
	if port == 0 {
		port = defaultPort
	}
	host := spec.Address
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	root := url.URL{
		Scheme: scheme,
		Host:   host + ":" + strconv.Itoa(port),
	}
	u := root
	u.Path = nxapiPath
	httpClient := opts.HTTPClient
	if httpClient == nil {
		timeout := opts.DefaultTimeout
		if timeout == 0 {
			timeout = 5 * time.Minute
		}
		httpClient = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		}
	}
	return &nxapiClient{
		rootURL:     root.String(),
		baseURL:     u.String(),
		username:    spec.Username,
		password:    spec.Password,
		client:      httpClient,
		sessionLock: opts.SessionLock,
	}, nil
}

type nxapiRequest struct {
	InsAPI nxapiRequestBody `json:"ins_api"`
}

type nxapiRequestBody struct {
	Version      string `json:"version"`
	Type         string `json:"type"`
	Chunk        string `json:"chunk"`
	SID          string `json:"sid"`
	Input        string `json:"input"`
	OutputFormat string `json:"output_format"`
}

type nxapiResponse struct {
	InsAPI struct {
		Outputs struct {
			Output json.RawMessage `json:"output"`
		} `json:"outputs"`
	} `json:"ins_api"`
}

type nxapiOutput struct {
	Input string          `json:"input"`
	Code  string          `json:"code"`
	Msg   string          `json:"msg"`
	Body  json.RawMessage `json:"body"`
}

func (c *nxapiClient) show(ctx context.Context, command string) (string, error) {
	return c.exec(ctx, "cli_show_ascii", command)
}

func (c *nxapiClient) conf(ctx context.Context, commands ...string) (string, error) {
	return c.exec(ctx, "cli_conf", strings.Join(commands, " ; "))
}

func (c *nxapiClient) exec(ctx context.Context, typ, input string) (string, error) {
	unlock := c.lockSession()
	defer unlock()
	errorContext := nxapiErrorContext(typ, input)
	payload := nxapiRequest{InsAPI: nxapiRequestBody{
		Version:      nxapiVersion,
		Type:         typ,
		Chunk:        "0",
		SID:          "1",
		Input:        input,
		OutputFormat: "json",
	}}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", errorContext, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("%s: read response: %w", errorContext, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isNXAPIConfigType(typ) {
			// A cli_conf request can contain resolved Kubernetes Secret values
			// in Docker run options, and an HTTP error body can echo the request.
			// Keep both out of errors that flow to controller logs and Events.
			return "", fmt.Errorf("%s: HTTP %d", errorContext, resp.StatusCode)
		}
		return "", fmt.Errorf("%s: HTTP %d: %s", errorContext, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	out, err := parseNXAPIResponse(raw, typ)
	if err != nil {
		return "", fmt.Errorf("%s: %w", errorContext, err)
	}
	return out, nil
}

func nxapiErrorContext(typ, input string) string {
	if isNXAPIConfigType(typ) {
		return fmt.Sprintf("nxapi %s", typ)
	}
	return fmt.Sprintf("nxapi %s %q", typ, input)
}

func isNXAPIConfigType(typ string) bool {
	return strings.EqualFold(typ, "cli_conf")
}

func parseNXAPIResponse(raw []byte, typ string) (string, error) {
	var resp nxapiResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if len(resp.InsAPI.Outputs.Output) == 0 {
		return "", nil
	}
	outputs, err := decodeNXAPIOutputs(resp.InsAPI.Outputs.Output)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, out := range outputs {
		body := bodyToString(out.Body)
		if out.Code != "" && out.Code != "200" {
			return "", nxapiOutputError(typ, out, body)
		}
		// NX-API can return an HTTP 200 and a successful envelope while the
		// configuration command itself failed. NX-OS reports that semantic
		// failure in a cli_conf string body beginning with "ERROR:". Keep the
		// check type-scoped so operational show output containing the same text
		// remains valid data.
		if isNXAPIConfigType(typ) && nxapiCLIErrorBody(body) {
			return "", nxapiOutputError(typ, out, body)
		}
		if body != "" {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func nxapiOutputError(typ string, out nxapiOutput, body string) error {
	if isNXAPIConfigType(typ) {
		// Never include cli_conf input or body: app-hosting configuration can
		// carry resolved SecretKeyRef values, and NX-API may echo input in any
		// free-form response field. Only a three-digit protocol status is safe.
		if code := nxapiNumericCode(out.Code); code != "" {
			return fmt.Errorf("configuration command failed: code=%s", code)
		}
		return fmt.Errorf("configuration command failed")
	}
	return fmt.Errorf("command %q failed: code=%s msg=%s body=%s",
		out.Input, out.Code, out.Msg, body)
}

func nxapiNumericCode(code string) string {
	code = strings.TrimSpace(code)
	if len(code) != 3 {
		return ""
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return code
}

func nxapiCLIErrorBody(body string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(body)), "ERROR:")
}

func (c *nxapiClient) lockSession() func() {
	if c.sessionLock != nil {
		c.sessionLock.Lock()
		return c.sessionLock.Unlock
	}
	c.mu.Lock()
	return c.mu.Unlock
}

func (c *nxapiClient) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return http.DefaultClient
}

func (c *nxapiClient) rootEndpoint(path string) string {
	root := strings.TrimRight(c.rootURL, "/")
	if root == "" {
		root = strings.TrimSuffix(strings.TrimRight(c.baseURL, "/"), nxapiPath)
	}
	return root + path
}

func decodeNXAPIOutputs(raw json.RawMessage) ([]nxapiOutput, error) {
	var one nxapiOutput
	if err := json.Unmarshal(raw, &one); err == nil {
		return []nxapiOutput{one}, nil
	}
	var many []nxapiOutput
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, err
	}
	return many, nil
}

func bodyToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err == nil {
		return pretty.String()
	}
	return string(raw)
}
