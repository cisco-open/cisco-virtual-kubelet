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

package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func NewNetworkClient(baseURL string, auth *ClientAuth, tlsConfig *tls.Config, timeout time.Duration) (NetworkClient, error) {

	ctype := "restconf"
	switch ctype {
	case "restconf":
		return NewClientRestconfClient(baseURL, auth, tlsConfig, timeout), nil
	default:
		return nil, fmt.Errorf("unsupported device type")
	}
}

func NewClientRestconfClient(baseURL string, auth *ClientAuth, tlsConfig *tls.Config, timeout time.Duration) *RestconfClient {
	username := ""
	password := ""

	if auth != nil {
		if auth.Username != "" {
			username = auth.Username
		}
		if auth.Password != "" {
			password = auth.Password
		}
	}

	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &RestconfClient{
		BaseURL:    baseURL,
		HTTPClient: httpClient,
		Username:   username,
		Password:   password,
	}
}

// RestconfClient implements the NetworkClient interface for RESTconf
type RestconfClient struct {
	BaseURL    string
	HTTPClient *http.Client
	Username   string
	Password   string
}

const maxRESTCONFResponseBytes int64 = 8 << 20

// RESTCONFError reports safe response metadata for a failed RESTCONF request.
// Response bodies are deliberately not retained because devices can echo
// request payloads containing credentials or app-hosting environment values.
type RESTCONFError struct {
	StatusCode int
	Status     string
	ErrorTags  []string
}

func (e *RESTCONFError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.ErrorTags) > 0 {
		return fmt.Sprintf("request failed with status %s (RESTCONF error-tag %s)", e.Status, strings.Join(e.ErrorTags, ","))
	}
	return fmt.Sprintf("request failed with status %s", e.Status)
}

// HasRESTCONFErrorTag reports whether err contains a parsed RESTCONF error-tag.
func HasRESTCONFErrorTag(err error, tag string) bool {
	var restErr *RESTCONFError
	if !errors.As(err, &restErr) || restErr == nil {
		return false
	}
	for _, candidate := range restErr.ErrorTags {
		if candidate == tag {
			return true
		}
	}
	return false
}

// IsRESTCONFStatus reports whether err contains the given HTTP status code.
func IsRESTCONFStatus(err error, statusCode int) bool {
	var restErr *RESTCONFError
	return errors.As(err, &restErr) && restErr != nil && restErr.StatusCode == statusCode
}

func (c *RestconfClient) Get(ctx context.Context, path string, result any, unmarshal func([]byte, any) error) error {
	return c.doRequest(ctx, "GET", path, nil, result, nil, unmarshal)
}

func (c *RestconfClient) Post(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	return c.doRequest(ctx, "POST", path, payload, nil, marshal, nil)
}

func (c *RestconfClient) Patch(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	return c.doRequest(ctx, "PATCH", path, payload, nil, marshal, nil)
}

func (c *RestconfClient) Put(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	return c.doRequest(ctx, "PUT", path, payload, nil, marshal, nil)
}

func (c *RestconfClient) Delete(ctx context.Context, path string) error {
	return c.doRequest(ctx, "DELETE", path, nil, nil, nil, nil)
}

func (c *RestconfClient) doRequest(ctx context.Context, method, path string, payload any, result any, marshal func(any) ([]byte, error), unmarshal func([]byte, any) error) error {
	var body io.Reader
	if payload != nil && marshal != nil {
		data, err := marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal failed: %w", err)
		}
		body = bytes.NewBuffer(data)

		log.G(ctx).WithFields(log.Fields{
			"method":     method,
			"path":       path,
			"body_bytes": len(data),
		}).Debug("Sending RESTCONF request")
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &RESTCONFError{
			StatusCode: resp.StatusCode,
			Status:     canonicalHTTPStatus(resp.StatusCode),
			ErrorTags:  restconfErrorTags(errBody),
		}
	}

	if result != nil && unmarshal != nil {
		data, err := readLimitedRESTCONFBody(resp.Body)
		if err != nil {
			return fmt.Errorf("read RESTCONF response for %s %s failed: %w", method, path, err)
		}
		log.G(ctx).WithFields(log.Fields{
			"method":         method,
			"path":           path,
			"status_code":    resp.StatusCode,
			"response_bytes": len(data),
		}).Debug("Received RESTCONF response")
		if err := unmarshal(data, result); err != nil {
			return fmt.Errorf("decode RESTCONF response for %s %s failed; response body omitted", method, path)
		}
		return nil
	}

	return nil
}

func readLimitedRESTCONFBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxRESTCONFResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxRESTCONFResponseBytes {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxRESTCONFResponseBytes)
	}
	return data, nil
}

func restconfErrorTags(body []byte) []string {
	var document map[string]json.RawMessage
	if len(body) == 0 || json.Unmarshal(body, &document) != nil {
		return nil
	}
	seen := map[string]struct{}{}
	tags := make([]string, 0, 1)
	for _, envelopeName := range []string{"ietf-restconf:errors", "errors"} {
		rawEnvelope, ok := document[envelopeName]
		if !ok {
			continue
		}
		var envelope struct {
			Errors []struct {
				Tag string `json:"error-tag"`
			} `json:"error"`
		}
		if json.Unmarshal(rawEnvelope, &envelope) != nil {
			continue
		}
		for _, item := range envelope.Errors {
			if _, allowed := standardRESTCONFErrorTags[item.Tag]; !allowed {
				continue
			}
			if _, exists := seen[item.Tag]; exists {
				continue
			}
			seen[item.Tag] = struct{}{}
			tags = append(tags, item.Tag)
		}
	}
	sort.Strings(tags)
	return tags
}

func canonicalHTTPStatus(statusCode int) string {
	if statusText := http.StatusText(statusCode); statusText != "" {
		return fmt.Sprintf("%d %s", statusCode, statusText)
	}
	return fmt.Sprintf("%d", statusCode)
}

var standardRESTCONFErrorTags = map[string]struct{}{
	"access-denied":           {},
	"bad-attribute":           {},
	"bad-element":             {},
	"data-exists":             {},
	"data-missing":            {},
	"in-use":                  {},
	"invalid-value":           {},
	"lock-denied":             {},
	"malformed-message":       {},
	"missing-attribute":       {},
	"missing-element":         {},
	"operation-failed":        {},
	"operation-not-supported": {},
	"partial-operation":       {},
	"resource-denied":         {},
	"rollback-failed":         {},
	"too-big":                 {},
	"unknown-attribute":       {},
	"unknown-element":         {},
	"unknown-namespace":       {},
}
