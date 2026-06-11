// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package aci

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
	defaultAPICAPIPort                  = 443
	defaultAPICAPIRequestTimeoutSeconds = 60
)

// LoginInfo captures the APIC aaaLogin attributes CVK needs for health and
// troubleshooting.
type LoginInfo struct {
	Token                 string `json:"token"`
	Version               string `json:"version"`
	UserName              string `json:"userName"`
	RefreshTimeoutSeconds string `json:"refreshTimeoutSeconds"`
	RestTimeoutSeconds    string `json:"restTimeoutSeconds"`
}

// TopSystem is the topSystem payload reduced to the fields CVK reports.
type TopSystem struct {
	DN          string `json:"dn"`
	Name        string `json:"name"`
	Serial      string `json:"serial"`
	Version     string `json:"version"`
	Role        string `json:"role"`
	State       string `json:"state"`
	OOBMgmtAddr string `json:"oobMgmtAddr"`
	INBMgmtAddr string `json:"inbMgmtAddr"`
}

// FabricNode captures APIC fabricNode inventory for health and telemetry.
type FabricNode struct {
	DN       string `json:"dn"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Serial   string `json:"serial"`
	Model    string `json:"model"`
	Version  string `json:"version"`
	FabricSt string `json:"fabricSt"`
}

// APICInfo is a compact controller and fabric snapshot used by node health.
type APICInfo struct {
	Login  LoginInfo
	System TopSystem
	Nodes  []FabricNode
}

// ManagedObject is a generic APIC managed object.
type ManagedObject struct {
	Class      string
	Attrs      map[string]string
	Children   []any
	Attributes map[string]any
}

type apicResponse struct {
	TotalCount string           `json:"totalCount"`
	IMData     []map[string]any `json:"imdata"`
}

// APIClient is the small APIC REST surface used by the ACI driver and
// NetAsCode applier.
type APIClient struct {
	baseURL  *url.URL
	username string
	password string
	http     *http.Client

	mu    sync.Mutex
	token string
}

func NewAPIClientFromSpec(spec *v1alpha1.DeviceSpec, password string) (*APIClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("apic api: nil device spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("apic api: device address is required")
	}
	if spec.Username == "" {
		return nil, fmt.Errorf("apic api: username is required")
	}
	if password == "" {
		password = spec.Password
	}
	if password == "" {
		return nil, fmt.Errorf("apic api: password is required")
	}
	base, err := apicAPIBaseURL(spec)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: apicAPIInsecureSkipVerify(spec)} // #nosec G402 - lab APIC simulators commonly use a self-signed cert.
	return &APIClient{
		baseURL:  base,
		username: spec.Username,
		password: password,
		http: &http.Client{
			Timeout:   apicAPIRequestTimeout(spec),
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
	_, err := c.Info(ctx)
	return err
}

func (c *APIClient) Info(ctx context.Context) (*APICInfo, error) {
	login, err := c.LoginInfo(ctx)
	if err != nil {
		return nil, err
	}
	system, err := c.TopSystem(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := c.FabricNodes(ctx)
	if err != nil {
		return nil, err
	}
	return &APICInfo{Login: *login, System: *system, Nodes: nodes}, nil
}

func (c *APIClient) LoginInfo(ctx context.Context) (*LoginInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authenticateLocked(ctx, false)
}

func (c *APIClient) TopSystem(ctx context.Context) (*TopSystem, error) {
	items, err := c.ListClass(ctx, "topSystem")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("apic api topSystem: no items returned")
	}
	return topSystemFromAttrs(items[0].Attrs), nil
}

func (c *APIClient) FabricNodes(ctx context.Context) ([]FabricNode, error) {
	items, err := c.ListClass(ctx, "fabricNode")
	if err != nil {
		return nil, err
	}
	out := make([]FabricNode, 0, len(items))
	for _, item := range items {
		out = append(out, fabricNodeFromAttrs(item.Attrs))
	}
	return out, nil
}

func (c *APIClient) ListClass(ctx context.Context, class string) ([]ManagedObject, error) {
	raw, err := c.Get(ctx, "/api/node/class/"+url.PathEscape(class)+".json")
	if err != nil {
		return nil, err
	}
	resp, err := parseAPICResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("apic api parse class %s: %w", class, err)
	}
	out := make([]ManagedObject, 0, len(resp.IMData))
	for _, item := range resp.IMData {
		mo, ok := managedObjectFromMap(item)
		if !ok {
			continue
		}
		out = append(out, mo)
	}
	return out, nil
}

func (c *APIClient) GetMO(ctx context.Context, dn string, query string) ([]ManagedObject, error) {
	path := "/api/node/mo/" + strings.TrimLeft(dn, "/") + ".json"
	if query != "" {
		path += "?" + strings.TrimLeft(query, "?")
	}
	raw, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	resp, err := parseAPICResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("apic api parse mo %s: %w", dn, err)
	}
	out := make([]ManagedObject, 0, len(resp.IMData))
	for _, item := range resp.IMData {
		mo, ok := managedObjectFromMap(item)
		if !ok {
			continue
		}
		out = append(out, mo)
	}
	return out, nil
}

func (c *APIClient) PostMO(ctx context.Context, dn, class string, attrs map[string]string, children []any) error {
	if strings.TrimSpace(dn) == "" || strings.TrimSpace(class) == "" {
		return fmt.Errorf("apic api post managed object: dn and class are required")
	}
	bodyAttrs := map[string]string{}
	for k, v := range attrs {
		if strings.TrimSpace(v) != "" {
			bodyAttrs[k] = v
		}
	}
	bodyAttrs["dn"] = dn
	payload := map[string]any{
		class: map[string]any{
			"attributes": bodyAttrs,
		},
	}
	if len(children) > 0 {
		payload[class].(map[string]any)["children"] = children
	}
	_, err := c.Post(ctx, "/api/node/mo/"+strings.TrimLeft(dn, "/")+".json", payload)
	return err
}

func (c *APIClient) Get(ctx context.Context, path string) ([]byte, error) {
	return c.request(ctx, http.MethodGet, path, nil, true)
}

func (c *APIClient) Post(ctx context.Context, path string, body any) ([]byte, error) {
	return c.request(ctx, http.MethodPost, path, body, true)
}

func (c *APIClient) request(ctx context.Context, method, path string, body any, retryAuth bool) ([]byte, error) {
	if c == nil || c.baseURL == nil || c.http == nil {
		return nil, fmt.Errorf("apic api: uninitialised client")
	}
	if !strings.Contains(path, "/aaaLogin.json") {
		c.mu.Lock()
		_, err := c.authenticateLocked(ctx, false)
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	u, err := c.resolveURL(path)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("apic api marshal %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !strings.Contains(path, "/aaaLogin.json") {
		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		req.Header.Set("Cookie", "APIC-cookie="+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apic api %s %s: %w", method, u.Redacted(), err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, fmt.Errorf("apic api %s %s read response: %w", method, u.Redacted(), readErr)
	}
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && retryAuth && !strings.Contains(path, "/aaaLogin.json") {
		c.mu.Lock()
		c.token = ""
		_, err := c.authenticateLocked(ctx, true)
		c.mu.Unlock()
		if err != nil {
			return raw, err
		}
		return c.request(ctx, method, path, body, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, fmt.Errorf("apic api %s %s: HTTP %d: %s", method, u.Redacted(), resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := apicResponseError(raw); err != nil {
		return raw, fmt.Errorf("apic api %s %s: %w", method, u.Redacted(), err)
	}
	return raw, nil
}

func (c *APIClient) authenticateLocked(ctx context.Context, force bool) (*LoginInfo, error) {
	if c == nil || c.http == nil {
		return nil, fmt.Errorf("apic api: uninitialised client")
	}
	if c.token != "" && !force {
		return &LoginInfo{Token: c.token}, nil
	}
	u, err := c.resolveURL("/api/aaaLogin.json")
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"aaaUser": map[string]any{
			"attributes": map[string]string{
				"name": c.username,
				"pwd":  c.password,
			},
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apic api auth %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return nil, fmt.Errorf("apic api auth read response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("apic api auth %s: HTTP %d: %s", u.Redacted(), resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := apicResponseError(raw); err != nil {
		return nil, fmt.Errorf("apic api auth %s: %w", u.Redacted(), err)
	}
	login, err := loginInfoFromResponse(raw)
	if err != nil {
		return nil, err
	}
	if login.Token == "" {
		return nil, fmt.Errorf("apic api auth %s: missing token", u.Redacted())
	}
	c.token = login.Token
	return login, nil
}

func (c *APIClient) resolveURL(path string) (*url.URL, error) {
	if parsed, err := url.Parse(path); err == nil && parsed.IsAbs() {
		return parsed, nil
	}
	if c.baseURL == nil {
		return nil, fmt.Errorf("apic api: nil base URL")
	}
	if strings.Contains(path, "?") {
		parts := strings.SplitN(path, "?", 2)
		return c.baseURL.ResolveReference(&url.URL{Path: joinURLPath(c.baseURL.Path, parts[0]), RawQuery: parts[1]}), nil
	}
	return c.baseURL.ResolveReference(&url.URL{Path: joinURLPath(c.baseURL.Path, path)}), nil
}

func apicAPIBaseURL(spec *v1alpha1.DeviceSpec) (*url.URL, error) {
	if spec != nil && spec.APIC != nil && spec.APIC.API != nil && spec.APIC.API.BaseURL != "" {
		u, err := url.Parse(spec.APIC.API.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("apic api baseUrl: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("apic api baseUrl must include scheme and host")
		}
		return u, nil
	}
	port := defaultAPICAPIPort
	if spec != nil && spec.APIC != nil && spec.APIC.API != nil && spec.APIC.API.Port > 0 {
		port = spec.APIC.API.Port
	} else if spec != nil && spec.Port > 0 {
		port = spec.Port
	}
	return url.Parse("https://" + joinHostPort(spec.Address, port))
}

func apicAPIRequestTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultAPICAPIRequestTimeoutSeconds
	if spec != nil && spec.APIC != nil && spec.APIC.API != nil && spec.APIC.API.RequestTimeoutSeconds > 0 {
		seconds = spec.APIC.API.RequestTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func apicAPIInsecureSkipVerify(spec *v1alpha1.DeviceSpec) bool {
	if spec != nil && spec.APIC != nil && spec.APIC.API != nil {
		return spec.APIC.API.InsecureSkipVerify
	}
	return true
}

func loginInfoFromResponse(raw []byte) (*LoginInfo, error) {
	resp, err := parseAPICResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("apic api parse auth response: %w", err)
	}
	if len(resp.IMData) == 0 {
		return nil, fmt.Errorf("apic api auth response: no imdata")
	}
	mo, ok := managedObjectFromMap(resp.IMData[0])
	if !ok || mo.Class != "aaaLogin" {
		return nil, fmt.Errorf("apic api auth response: missing aaaLogin")
	}
	return &LoginInfo{
		Token:                 mo.Attrs["token"],
		Version:               mo.Attrs["version"],
		UserName:              mo.Attrs["userName"],
		RefreshTimeoutSeconds: mo.Attrs["refreshTimeoutSeconds"],
		RestTimeoutSeconds:    mo.Attrs["restTimeoutSeconds"],
	}, nil
}

func parseAPICResponse(raw []byte) (*apicResponse, error) {
	var resp apicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func apicResponseError(raw []byte) error {
	resp, err := parseAPICResponse(raw)
	if err != nil || len(resp.IMData) == 0 {
		return nil
	}
	for _, item := range resp.IMData {
		if errRaw, ok := item["error"]; ok {
			m, _ := errRaw.(map[string]any)
			attrs, _ := m["attributes"].(map[string]any)
			code, _ := attrs["code"].(string)
			text, _ := attrs["text"].(string)
			return fmt.Errorf("APIC error %s: %s", code, text)
		}
	}
	return nil
}

func managedObjectFromMap(item map[string]any) (ManagedObject, bool) {
	for class, raw := range item {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		attrs := stringMap(m["attributes"])
		attrsAny := anyMap(m["attributes"])
		children, _ := m["children"].([]any)
		return ManagedObject{Class: class, Attrs: attrs, Attributes: attrsAny, Children: children}, true
	}
	return ManagedObject{}, false
}

func stringMap(v any) map[string]string {
	out := map[string]string{}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, raw := range m {
		switch t := raw.(type) {
		case string:
			out[k] = t
		case fmt.Stringer:
			out[k] = t.String()
		default:
			if raw != nil {
				out[k] = fmt.Sprint(raw)
			}
		}
	}
	return out
}

func anyMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		out[k] = val
	}
	return out
}

func topSystemFromAttrs(attrs map[string]string) *TopSystem {
	return &TopSystem{
		DN:          attrs["dn"],
		Name:        attrs["name"],
		Serial:      attrs["serial"],
		Version:     attrs["version"],
		Role:        attrs["role"],
		State:       attrs["state"],
		OOBMgmtAddr: attrs["oobMgmtAddr"],
		INBMgmtAddr: attrs["inbMgmtAddr"],
	}
}

func fabricNodeFromAttrs(attrs map[string]string) FabricNode {
	return FabricNode{
		DN:       attrs["dn"],
		ID:       attrs["id"],
		Name:     attrs["name"],
		Role:     attrs["role"],
		Serial:   attrs["serial"],
		Model:    attrs["model"],
		Version:  attrs["version"],
		FabricSt: attrs["fabricSt"],
	}
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
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}
