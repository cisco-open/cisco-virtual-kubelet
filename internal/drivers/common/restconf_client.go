package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
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

func (c *RestconfClient) Get(ctx context.Context, path string, result any, unmarshal func([]byte, any) error) error {
	return c.doRequest(ctx, "GET", path, nil, result, nil, unmarshal)
}

func (c *RestconfClient) Post(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	return c.doRequest(ctx, "POST", path, payload, nil, marshal, nil)
}

func (c *RestconfClient) Patch(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	return c.doRequest(ctx, "PATCH", path, payload, nil, marshal, nil)
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

		// log.G(ctx).WithFields(log.Fields{
		// 	"body": string(data),
		// }).Info("Sending Body")
		// fmt.Print(path)
		// fmt.Print(string(data))
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

	if resp.StatusCode >= 300 {
		return fmt.Errorf("request failed with status %s", resp.Status)
	}

	if result != nil && unmarshal != nil {
		log.G(ctx).Info("Checking response ...")
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return unmarshal(data, result)
	}

	return nil
}
