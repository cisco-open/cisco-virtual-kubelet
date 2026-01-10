package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

type ClientAuth struct {
	Method   string
	Username string
	Password string
}

type RESTClient struct {
	baseURL    string
	auth       *ClientAuth
	httpClient *http.Client
	tlsConfig  *tls.Config
	timeout    time.Duration
}

func NewClientRestClient(baseURL string, auth *ClientAuth, tlsConfig *tls.Config, timeout time.Duration) *RESTClient {
	return &RESTClient{
		baseURL:    baseURL,
		auth:       auth,
		httpClient: &http.Client{},
		tlsConfig:  tlsConfig,
		timeout:    timeout,
	}
}

func (c *RESTClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Centralized Headers and Auth
	req.SetBasicAuth(c.auth.Username, c.auth.Password)
	req.Header.Set("Accept", "application/yang-data+json")
	req.Header.Set("Content-Type", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}

	// Centralized Error Handling
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}

func (c *RESTClient) Get(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *RESTClient) Post(ctx context.Context, path, body string) error {

	// body, err := json.Marshal(data)
	// if err != nil {
	// 	return err
	// }
	log.G(ctx).Debugf("POST JSON body: %s", string(body))

	resp, err := c.do(ctx, "POST", path, strings.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *RESTClient) Patch(ctx context.Context, path string, data interface{}) error {
	body, _ := json.Marshal(data)
	resp, err := c.do(ctx, "PATCH", path, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// func validateDeviceCapabilities(ctx context.Context) error {
// 	return nil
// }