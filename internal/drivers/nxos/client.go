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
)

const (
	nxapiVersion = "1.0"
	nxapiPath    = "/ins"
)

type nxapiClient struct {
	baseURL     string
	username    string
	password    string
	client      *http.Client
	sessionLock *sync.Mutex
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
	tlsConfig := &tls.Config{} // #nosec G402 - InsecureSkipVerify is operator-controlled for lab devices.
	if spec.TLS != nil && spec.TLS.Enabled {
		scheme = "https"
		defaultPort = 443
		tlsConfig.InsecureSkipVerify = spec.TLS.InsecureSkipVerify // #nosec G402
	}
	port := spec.Port
	if port == 0 {
		port = defaultPort
	}
	host := spec.Address
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	u := url.URL{
		Scheme: scheme,
		Host:   host + ":" + strconv.Itoa(port),
		Path:   nxapiPath,
	}
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
	if c.sessionLock != nil {
		c.sessionLock.Lock()
		defer c.sessionLock.Unlock()
	}
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

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("nxapi %s %q: %w", typ, input, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("nxapi %s %q: read response: %w", typ, input, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("nxapi %s %q: HTTP %d: %s", typ, input, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	out, err := parseNXAPIResponse(raw)
	if err != nil {
		return "", fmt.Errorf("nxapi %s %q: %w", typ, input, err)
	}
	return out, nil
}

func parseNXAPIResponse(raw []byte) (string, error) {
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
		if out.Code != "" && out.Code != "200" {
			return "", fmt.Errorf("command %q failed: code=%s msg=%s body=%s",
				out.Input, out.Code, out.Msg, bodyToString(out.Body))
		}
		body := bodyToString(out.Body)
		if body != "" {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, "\n"), nil
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
