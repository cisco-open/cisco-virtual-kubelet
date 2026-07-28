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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

const (
	dmeLoginPath = "/api/aaaLogin.json"
	dmeMOPath    = "/api/mo/"
)

type dmeEnvelope struct {
	TotalCount string                       `json:"totalCount,omitempty"`
	IMData     []map[string]json.RawMessage `json:"imdata,omitempty"`
}

type dmeMO struct {
	Attributes map[string]any               `json:"attributes,omitempty"`
	Children   []map[string]json.RawMessage `json:"children,omitempty"`
}

type dmeErrorMO struct {
	Attributes struct {
		Code string `json:"code"`
		Text string `json:"text"`
	} `json:"attributes"`
}

func (c *nxapiClient) dmeGet(ctx context.Context, dn string, query url.Values) ([]byte, error) {
	return c.dmeRequest(ctx, http.MethodGet, dn, query, nil)
}

func (c *nxapiClient) dmePost(ctx context.Context, dn string, body []byte) error {
	_, err := c.dmeRequest(ctx, http.MethodPost, dn, nil, body)
	return err
}

func (c *nxapiClient) dmePut(ctx context.Context, dn string, body []byte) error {
	_, err := c.dmeRequest(ctx, http.MethodPut, dn, nil, body)
	return err
}

func (c *nxapiClient) dmeDelete(ctx context.Context, dn string) error {
	_, err := c.dmeRequest(ctx, http.MethodDelete, dn, nil, nil)
	return err
}

func (c *nxapiClient) dmeRequest(ctx context.Context, method, dn string, query url.Values, body []byte) ([]byte, error) {
	unlock := c.lockSession()
	defer unlock()
	if err := c.dmeLoginLocked(ctx); err != nil {
		return nil, err
	}
	raw, err := c.dmeRequestLocked(ctx, method, dn, query, body)
	if isDMEAuthError(err) {
		c.dmeCookies = nil
		// DME writes are non-transactional. An authentication failure may
		// arrive after the device accepted some or all of a mutation, so it
		// is unsafe to replay POST/PUT/DELETE automatically. The next
		// reconciliation logs in again and verifies observed state first.
		if method != http.MethodGet {
			return raw, err
		}
		if loginErr := c.dmeLoginLocked(ctx); loginErr != nil {
			return nil, loginErr
		}
		raw, err = c.dmeRequestLocked(ctx, method, dn, query, body)
	}
	return raw, err
}

func (c *nxapiClient) dmeLoginLocked(ctx context.Context) error {
	if len(c.dmeCookies) > 0 {
		return nil
	}
	payload := map[string]any{
		"aaaUser": map[string]any{
			"attributes": map[string]string{
				"name": c.username,
				"pwd":  c.password,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	rest, err := c.restClient()
	if err != nil {
		return err
	}
	resp, err := rest.DoRaw(ctx, configtransport.RESTRequest{
		Method: http.MethodPost,
		Path:   dmeLoginPath,
		Body:   body,
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
	})
	if err != nil {
		return wrapDMERequestError(http.MethodPost, "", err)
	}
	raw := resp.Body
	if err := dmeResponseErrorFor(http.MethodPost, "", raw); err != nil {
		return err
	}
	c.dmeCookies = (&http.Response{Header: resp.Header}).Cookies()
	if len(c.dmeCookies) == 0 {
		if token := dmeLoginToken(raw); token != "" {
			c.dmeCookies = []*http.Cookie{
				{Name: "nxapi_auth", Value: token},
				{Name: "APIC-cookie", Value: token},
			}
		}
	}
	return nil
}

func (c *nxapiClient) dmeRequestLocked(ctx context.Context, method, dn string, query url.Values, body []byte) ([]byte, error) {
	path, err := dmePath(dn)
	if err != nil {
		return nil, err
	}
	rest, err := c.restClient()
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Accept": "application/json"}
	if body != nil {
		headers["Content-Type"] = "application/json"
	}
	if cookieHeader := dmeCookieHeader(c.dmeCookies); cookieHeader != "" {
		headers["Cookie"] = cookieHeader
	}
	raw, err := rest.Do(ctx, configtransport.RESTRequest{
		Method:  method,
		Path:    path,
		Query:   query,
		Body:    body,
		Headers: headers,
	})
	if err != nil {
		return nil, wrapDMERequestError(method, dn, err)
	}
	if err := dmeResponseErrorFor(method, dn, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *nxapiClient) restClient() (*configtransport.RESTClient, error) {
	root := strings.TrimRight(c.rootURL, "/")
	if root == "" {
		root = strings.TrimSuffix(strings.TrimRight(c.baseURL, "/"), nxapiPath)
	}
	return configtransport.NewRESTClient(root, configtransport.RESTClientOptions{
		HTTPClient: c.httpClient(),
		Auth: configtransport.RESTAuth{
			Username: c.username,
			Password: c.password,
		},
	})
}

func dmePath(dn string) (string, error) {
	dn = strings.TrimSpace(dn)
	if dn == "" {
		return "", fmt.Errorf("nxapi dme: empty DN")
	}
	dn = strings.TrimPrefix(strings.TrimPrefix(dn, "/"), "api/mo/")
	dn = strings.TrimSuffix(dn, ".json")
	return dmeMOPath + dn + ".json", nil
}

func (c *nxapiClient) dmeURL(dn string, query url.Values) (string, error) {
	path, err := dmePath(dn)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(c.rootEndpoint(path))
	if err != nil {
		return "", err
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String(), nil
}

func dmeCookieHeader(cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func dmeLoginToken(raw []byte) string {
	var top map[string]dmeMO
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}
	if login, ok := top["aaaLogin"]; ok {
		return stringAttr(login.Attributes, "token")
	}
	var env dmeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	for _, item := range env.IMData {
		if raw, ok := item["aaaLogin"]; ok {
			var login dmeMO
			if err := json.Unmarshal(raw, &login); err == nil {
				return stringAttr(login.Attributes, "token")
			}
		}
	}
	return ""
}

func isDMEAuthError(err error) bool {
	if err == nil {
		return false
	}
	var classified interface{ AuthFailure() bool }
	if errors.As(err, &classified) && classified.AuthFailure() {
		return true
	}
	if configtransport.IsAuthRESTError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "http 401") ||
		strings.Contains(msg, "http 403") ||
		strings.Contains(msg, "bad token") ||
		strings.Contains(msg, "authentication")
}

func collectDMEClassAttrs(raw []byte, class string) []map[string]any {
	var env dmeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	var out []map[string]any
	for _, item := range env.IMData {
		out = append(out, collectDMEClassAttrsFromItem(item, class)...)
	}
	return out
}

func collectDMEClassAttrsFromItem(item map[string]json.RawMessage, class string) []map[string]any {
	var out []map[string]any
	for gotClass, raw := range item {
		var mo dmeMO
		if err := json.Unmarshal(raw, &mo); err != nil {
			continue
		}
		if gotClass == class {
			out = append(out, mo.Attributes)
		}
		for _, child := range mo.Children {
			out = append(out, collectDMEClassAttrsFromItem(child, class)...)
		}
	}
	return out
}

func stringAttr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	switch v := attrs[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
