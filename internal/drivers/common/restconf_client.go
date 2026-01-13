package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/openconfig/ygot/ygot"
)

// NetworkClient defines the generic interface for any backend (RESTconf, Netconf, etc.)
type NetworkClient interface {
	Get(ctx context.Context, path string, result ygot.GoStruct) error
	Post(ctx context.Context, path string, payload ygot.GoStruct) error
	Patch(ctx context.Context, path string, payload ygot.GoStruct) error
	Delete(ctx context.Context, path string) error
}

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
	return &RestconfClient{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{},
		Username:   "admin",
		Password:   "admin",
	}
}

// RestconfClient implements the NetworkClient interface for RESTconf
type RestconfClient struct {
	BaseURL    string
	HTTPClient *http.Client
	Username   string
	Password   string
}

// Post creates a new resource (201 Created on success)
func (c *RestconfClient) Post(ctx context.Context, path string, payload ygot.GoStruct) error {
	return c.doRequest(ctx, "POST", path, payload, nil)
}

// Patch updates/merges an existing resource (204 No Content on success)
func (c *RestconfClient) Patch(ctx context.Context, path string, payload ygot.GoStruct) error {
	return c.doRequest(ctx, "PATCH", path, payload, nil)
}

// Delete removes a resource
func (c *RestconfClient) Delete(ctx context.Context, path string) error {
	return c.doRequest(ctx, "DELETE", path, nil, nil)
}

// Get retrieves data and unmarshals it into the provided result struct
func (c *RestconfClient) Get(ctx context.Context, path string, result ygot.GoStruct) error {
	return c.doRequest(ctx, "GET", path, nil, result)
}

// doRequest handles the underlying HTTP logic and ygot marshalling/unmarshalling
func (c *RestconfClient) doRequest(ctx context.Context, method, path string, payload ygot.GoStruct, result ygot.GoStruct) error {
	var body io.Reader
	if payload != nil {
		// Render ygot struct to RFC7951 JSON for RESTconf
		jsonPayload, err := ygot.EmitJSON(payload, &ygot.EmitJSONConfig{
			Format: ygot.RFC7951,
			RFC7951Config: &ygot.RFC7951JSONConfig{
				AppendModuleName: true,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %w", err)
		}
		body = bytes.NewBufferString(jsonPayload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}

	// RESTconf specific headers
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("request failed with status %s", resp.Status)
	}

	// For GET requests, unmarshal the response into the provided ygot struct
	if result != nil && method == "GET" {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if hm, ok := result.(*HostMeta); ok {
			decoder := xml.NewDecoder(bytes.NewReader(data))
			// Strict: false allows the parser to handle the namespace
			// mismatch without crashing
			decoder.Strict = false
			if err := decoder.Decode(hm); err != nil {
				return fmt.Errorf("failed to unmarshal host-meta: %w", err)
			}
			return nil
		}

		// 2. Otherwise, treat it as a ygot YANG struct
		// Use the Unmarshal function generated from your YANG models
		if err := Unmarshal(data, result); err != nil {
			return fmt.Errorf("ygot unmarshal failed: %w", err)
		}
	}

	return nil
}
