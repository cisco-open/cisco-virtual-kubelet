// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

type endlessXReader struct{}

func (endlessXReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestParseNXAPIResponseSingleOutput(t *testing.T) {
	raw := []byte(`{"ins_api":{"outputs":{"output":{"input":"show version","code":"200","msg":"Success","body":"NXOS: version 10.3(9)\n"}}}}`)
	got, err := parseNXAPIResponse(raw, "cli_show_ascii")
	if err != nil {
		t.Fatalf("parseNXAPIResponse: %v", err)
	}
	if got != "NXOS: version 10.3(9)\n" {
		t.Fatalf("unexpected body %q", got)
	}
}

func TestParseNXAPIResponseRejectsCLIConfErrorBody(t *testing.T) {
	raw := []byte(`{"ins_api":{"outputs":{"output":{"input":"app-hosting activate appid cvk0000_deadbeef","code":"200","msg":"Success","body":"  Error: Activate failed: app needs app-vnic configuration\n"}}}}`)
	_, err := parseNXAPIResponse(raw, "cli_conf")
	if err == nil {
		t.Fatal("parseNXAPIResponse accepted a cli_conf semantic error body")
	}
	for _, want := range []string{"configuration command failed", "code=200"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error=%q, want %q", err, want)
		}
	}
	for _, leaked := range []string{"app-hosting activate", "Activate failed", "app-vnic"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("configuration error exposed device input/body %q: %v", leaked, err)
		}
	}
}

func TestParseNXAPIResponseRequiresExplicitSuccess(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty document", raw: `{}`},
		{name: "null output", raw: `{"ins_api":{"outputs":{"output":null}}}`},
		{name: "empty output object", raw: `{"ins_api":{"outputs":{"output":{}}}}`},
		{name: "missing code with body", raw: `{"ins_api":{"outputs":{"output":{"body":"configured"}}}}`},
		{name: "empty output array", raw: `{"ins_api":{"outputs":{"output":[]}}}`},
	}
	for _, typ := range []string{"cli_conf", "cli_show_ascii"} {
		for _, tt := range tests {
			t.Run(typ+"/"+tt.name, func(t *testing.T) {
				_, err := parseNXAPIResponse([]byte(tt.raw), typ)
				if err == nil {
					t.Fatalf("parseNXAPIResponse(%s, %s) succeeded, want fail-closed error", tt.raw, typ)
				}
			})
		}
	}

	valid := []byte(`{"ins_api":{"outputs":{"output":{"code":"200","msg":"Success","body":""}}}}`)
	for _, typ := range []string{"cli_conf", "cli_show_ascii"} {
		if _, err := parseNXAPIResponse(valid, typ); err != nil {
			t.Fatalf("parseNXAPIResponse rejected explicit 200 success for %s: %v", typ, err)
		}
	}
}

func TestNXAPIConfRejectsSuccessEnvelopeWithErrorBody(t *testing.T) {
	const secret = "never-log-this-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.InsAPI.Type != "cli_conf" {
			t.Fatalf("type=%q, want cli_conf", req.InsAPI.Type)
		}
		response := map[string]any{"ins_api": map[string]any{"outputs": map[string]any{"output": map[string]any{
			"input": req.InsAPI.Input,
			"code":  "200",
			"msg":   "rejected " + secret,
			"body":  "ERROR: rejected --env TOKEN='" + secret + "'",
		}}}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := &nxapiClient{baseURL: server.URL, client: server.Client()}
	_, err := client.conf(context.Background(), "configure terminal", `app-hosting appid cvk-test ; app-resource docker ; run-opts 1 "--env TOKEN='`+secret+`'"`)
	if err == nil {
		t.Fatal("conf accepted a success envelope carrying a CLI error body")
	}
	for _, want := range []string{"nxapi cli_conf", "configuration command failed", "code=200"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error=%q, want %q", err, want)
		}
	}
	for _, leaked := range []string{secret, "--env", "run-opts", "app-hosting appid"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("configuration error leaked %q: %v", leaked, err)
		}
	}
}

func TestNXAPIConfigErrorOnlyReportsNumericCode(t *testing.T) {
	const secret = "12345678901234567890"
	raw := []byte(`{"ins_api":{"outputs":{"output":{"input":"configure terminal","code":"` + secret + `","msg":"` + secret + `","body":"ERROR: ` + secret + `"}}}}`)
	_, err := parseNXAPIResponse(raw, "cli_conf")
	if err == nil {
		t.Fatal("parseNXAPIResponse accepted an invalid configuration response")
	}
	if err.Error() != "configuration command failed" {
		t.Fatalf("error=%q, want fixed safe message", err)
	}
}

func TestNXAPINumericCodeRequiresThreeASCIIDigits(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "200", want: "200"},
		{in: " 400 ", want: "400"},
		{in: "20a"},
		{in: "12"},
		{in: "12345678901234567890"},
	} {
		if got := nxapiNumericCode(tc.in); got != tc.want {
			t.Fatalf("nxapiNumericCode(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNXAPIConfHTTPErrorDoesNotExposeInputOrResponse(t *testing.T) {
	const secret = "never-log-http-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`request rejected: run-opts --env TOKEN='` + secret + `'`))
	}))
	defer server.Close()

	client := &nxapiClient{baseURL: server.URL, client: server.Client()}
	_, err := client.conf(context.Background(), `run-opts 1 "--env TOKEN='`+secret+`'"`)
	if err == nil {
		t.Fatal("conf accepted HTTP 400")
	}
	if !strings.Contains(err.Error(), "nxapi cli_conf: HTTP 400") {
		t.Fatalf("error=%q, want safe HTTP context", err)
	}
	for _, leaked := range []string{secret, "--env", "run-opts"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("HTTP configuration error leaked %q: %v", leaked, err)
		}
	}
}

func TestNXAPIShowErrorsDoNotExposeInputOrResponse(t *testing.T) {
	const sentinel = "NXAPI_SHOW_SECRET_SENTINEL_DO_NOT_EXPOSE"
	tests := []struct {
		name     string
		response func(http.ResponseWriter)
		want     string
	}{
		{
			name: "HTTP error",
			response: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("echoed run-opts --env TOKEN='" + sentinel + "'"))
			},
			want: "HTTP 400",
		},
		{
			name: "NX-API envelope error",
			response: func(w http.ResponseWriter) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ins_api": map[string]any{"outputs": map[string]any{"output": map[string]any{
					"input": "show app-hosting detail " + sentinel,
					"code":  "400",
					"msg":   "rejected " + sentinel,
					"body":  "run-opts --env TOKEN='" + sentinel + "'",
				}}}})
			},
			want: "command failed: code=400",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tt.response(w)
			}))
			defer server.Close()

			client := &nxapiClient{baseURL: server.URL, client: server.Client()}
			_, err := client.show(context.Background(), "show app-hosting detail appid "+sentinel)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("show err=%v, want safe context %q", err, tt.want)
			}
			for _, leaked := range []string{sentinel, "run-opts", "--env"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("show error exposed %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestNewNXAPIClientRejectsRedirects(t *testing.T) {
	const sentinel = "NXAPI_REDIRECT_SECRET_SENTINEL_DO_NOT_EXPOSE"
	var redirectedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case nxapiPath:
			http.Redirect(w, r, "/unexpected-target", http.StatusTemporaryRedirect)
		case "/unexpected-target":
			redirectedRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	client, err := newNXAPIClient(&ciskov1.DeviceSpec{
		Address:  u.Hostname(),
		Port:     port,
		Username: "admin",
		Password: "password",
	})
	if err != nil {
		t.Fatalf("newNXAPIClient: %v", err)
	}
	_, err = client.conf(context.Background(), "run-opts 1 --env TOKEN='"+sentinel+"'")
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("conf err=%v, want redirect rejection", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("redirect error exposed request body: %v", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("NX-API client followed redirect %d time(s)", got)
	}
}

func TestNXAPIRejectsOversizedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(w, endlessXReader{}, maxNXAPIResponseBytes+1)
	}))
	defer server.Close()

	client := &nxapiClient{baseURL: server.URL, client: server.Client()}
	_, err := client.show(context.Background(), "show app-hosting detail")
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("show err=%v, want response-size rejection", err)
	}
}

func TestParseNXAPIResponseAllowsErrorTextInShowOutput(t *testing.T) {
	raw := []byte(`{"ins_api":{"outputs":{"output":{"input":"show logging","code":"200","msg":"Success","body":"ERROR: this is device log content\n"}}}}`)
	got, err := parseNXAPIResponse(raw, "cli_show_ascii")
	if err != nil {
		t.Fatalf("parseNXAPIResponse: %v", err)
	}
	if got != "ERROR: this is device log content\n" {
		t.Fatalf("body=%q", got)
	}
}

func TestNXAPICLIErrorBodyRequiresLeadingErrorPrefix(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "exact", body: "ERROR: failed", want: true},
		{name: "case and whitespace", body: " \n Error: failed", want: true},
		{name: "embedded", body: "warning: prior ERROR: counter", want: false},
		{name: "empty", body: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nxapiCLIErrorBody(tc.body); got != tc.want {
				t.Fatalf("nxapiCLIErrorBody(%q)=%v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestNXAPIExecSessionLockCoversRequestConstruction(t *testing.T) {
	lock := &sync.Mutex{}
	lock.Lock()
	c := &nxapiClient{
		baseURL:     "http://[::1",
		client:      http.DefaultClient,
		sessionLock: lock,
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.exec(context.Background(), "cli_show_ascii", "show version")
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("exec returned before session lock was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	lock.Unlock()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("exec returned nil error for invalid URL")
		}
		if !strings.Contains(err.Error(), "missing ']'") {
			t.Fatalf("exec error=%q, want invalid URL parse error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after session lock was released")
	}
}

func TestNXAPIDMELoginTokenFallback(t *testing.T) {
	var sawCookie bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/aaaLogin.json":
			_, _ = w.Write([]byte(`{"aaaLogin":{"attributes":{"token":"token-from-body"}}}`))
		case "/api/mo/sys.json":
			cookie, err := r.Cookie("nxapi_auth")
			if err != nil {
				t.Fatalf("missing nxapi_auth cookie: %v", err)
			}
			if cookie.Value != "token-from-body" {
				t.Fatalf("cookie=%q", cookie.Value)
			}
			sawCookie = true
			_, _ = w.Write([]byte(`{"imdata":[{"topSystem":{"attributes":{"name":"leaf-01"}}}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := &nxapiClient{
		rootURL:  server.URL,
		baseURL:  server.URL + "/ins",
		username: "admin",
		password: "pw",
		client:   server.Client(),
	}
	raw, err := c.dmeGet(context.Background(), "sys", nil)
	if err != nil {
		t.Fatalf("dmeGet: %v", err)
	}
	if !sawCookie {
		t.Fatal("DME request did not use login token cookie")
	}
	if got := parseDMESystemHostname(raw); got != "leaf-01" {
		t.Fatalf("hostname=%q", got)
	}
}

func TestNXAPIDMEReadReauthenticatesAfterAuthFailure(t *testing.T) {
	var logins, reads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/aaaLogin.json":
			logins++
			token := "token-" + strconv.Itoa(logins)
			http.SetCookie(w, &http.Cookie{Name: "nxapi_auth", Value: token})
			_, _ = w.Write([]byte(`{"aaaLogin":{"attributes":{"token":"` + token + `"}}}`))
		case "/api/mo/sys.json":
			reads++
			if reads == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"imdata":[{"error":{"attributes":{"code":"401","text":"bad token"}}}]}`))
				return
			}
			cookie, err := r.Cookie("nxapi_auth")
			if err != nil || cookie.Value != "token-2" {
				t.Fatalf("reauthenticated cookie=%v err=%v", cookie, err)
			}
			_, _ = w.Write([]byte(`{"imdata":[{"topSystem":{"attributes":{"name":"leaf-01"}}}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := &nxapiClient{
		rootURL:  server.URL,
		baseURL:  server.URL + "/ins",
		username: "admin",
		password: "pw",
		client:   server.Client(),
	}
	raw, err := c.dmeGet(context.Background(), "sys", nil)
	if err != nil {
		t.Fatalf("dmeGet: %v", err)
	}
	if got := parseDMESystemHostname(raw); got != "leaf-01" {
		t.Fatalf("hostname=%q", got)
	}
	if logins != 2 || reads != 2 {
		t.Fatalf("logins=%d reads=%d, want 2/2", logins, reads)
	}
}

func TestNXAPIDMEReturnsErrorMO(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/aaaLogin.json":
			http.SetCookie(w, &http.Cookie{Name: "nxapi_auth", Value: "token"})
			_, _ = w.Write([]byte(`{"aaaLogin":{"attributes":{"token":"token"}}}`))
		case "/api/mo/sys.json":
			_, _ = w.Write([]byte(`{"imdata":[{"error":{"attributes":{"code":"400","text":"bad DME payload"}}}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := &nxapiClient{
		rootURL:  server.URL,
		baseURL:  server.URL + "/ins",
		username: "admin",
		password: "pw",
		client:   server.Client(),
	}
	err := c.dmePost(context.Background(), "sys", []byte(`{"topSystem":{"attributes":{"name":"leaf-01"}}}`))
	if err == nil {
		t.Fatal("dmePost accepted DME error response")
	}
	if !strings.Contains(err.Error(), "bad DME payload") || !strings.Contains(err.Error(), "code=400") {
		t.Fatalf("error=%q", err)
	}
	var dmeErr *DMEError
	if !errors.As(err, &dmeErr) {
		t.Fatalf("error %T does not retain DMEError", err)
	}
	if dmeErr.Category != DMEErrorValidation || dmeErr.Code != "400" ||
		dmeErr.Method != http.MethodPost || dmeErr.DN != "sys" {
		t.Fatalf("DMEError=%#v", dmeErr)
	}
}

func TestDMEErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		code     string
		text     string
		category DMEErrorCategory
	}{
		{name: "auth", code: "401", text: "authentication failed", category: DMEErrorAuth},
		{name: "retryable", code: "503", text: "device busy", category: DMEErrorRetryable},
		{name: "validation HTTP code", code: "400", text: "bad payload", category: DMEErrorValidation},
		{name: "validation DME code", code: "17", text: "Unknown class fmMissing", category: DMEErrorValidation},
		{name: "permanent", code: "122", text: "operation rejected", category: DMEErrorPermanent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(
				`{"imdata":[{"error":{"attributes":{"code":%q,"text":%q}}}]}`,
				tt.code,
				tt.text,
			))
			err := dmeResponseErrorFor(http.MethodGet, "sys", raw)
			var dmeErr *DMEError
			if !errors.As(err, &dmeErr) {
				t.Fatalf("error=%v does not retain DMEError", err)
			}
			if dmeErr.Category != tt.category {
				t.Fatalf("category=%q, want %q", dmeErr.Category, tt.category)
			}
			if got := dmeErr.Retryable(); got != (tt.category == DMEErrorRetryable) {
				t.Fatalf("Retryable()=%v", got)
			}
			if got := dmeErr.AuthFailure(); got != (tt.category == DMEErrorAuth) {
				t.Fatalf("AuthFailure()=%v", got)
			}
		})
	}
}

func TestDMEErrorClassificationPrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       DMEErrorCategory
	}{
		{
			name:       "auth detail outranks retryable HTTP status",
			statusCode: http.StatusServiceUnavailable,
			body: `{"imdata":[
				{"error":{"attributes":{"code":"503","text":"device busy"}}},
				{"error":{"attributes":{"code":"401","text":"bad token"}}}
			]}`,
			want: DMEErrorAuth,
		},
		{
			name:       "retryable detail outranks validation HTTP status",
			statusCode: http.StatusBadRequest,
			body:       `{"imdata":[{"error":{"attributes":{"code":"503","text":"device busy"}}}]}`,
			want:       DMEErrorRetryable,
		},
		{
			name:       "retryable HTTP status outranks validation detail",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"imdata":[{"error":{"attributes":{"code":"400","text":"invalid payload"}}}]}`,
			want:       DMEErrorRetryable,
		},
		{
			name: "auth detail outranks validation and permanent details",
			body: `{"imdata":[
				{"error":{"attributes":{"code":"122","text":"operation rejected"}}},
				{"error":{"attributes":{"code":"400","text":"invalid payload"}}},
				{"error":{"attributes":{"code":"403","text":"forbidden"}}}
			]}`,
			want: DMEErrorAuth,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.statusCode == 0 {
				err = dmeResponseErrorFor(http.MethodGet, "sys", []byte(tt.body))
			} else {
				err = wrapDMERequestError(http.MethodGet, "sys", &configtransport.RESTError{
					Method:     http.MethodGet,
					Path:       "/api/mo/sys.json",
					Status:     http.StatusText(tt.statusCode),
					StatusCode: tt.statusCode,
					Body:       tt.body,
				})
			}
			var dmeErr *DMEError
			if !errors.As(err, &dmeErr) {
				t.Fatalf("error=%v does not retain DMEError", err)
			}
			if dmeErr.Category != tt.want {
				t.Fatalf("category=%q, want %q; error=%v", dmeErr.Category, tt.want, err)
			}
		})
	}
}

func TestDMEErrorRedactsBoundsAndPreservesChain(t *testing.T) {
	t.Parallel()
	const secret = "DME_SECRET_SENTINEL"
	restErr := &configtransport.RESTError{
		Method:     http.MethodGet,
		Path:       "/api/mo/sys.json",
		Status:     "503 Service Unavailable",
		StatusCode: http.StatusServiceUnavailable,
		Body: `{"imdata":[{"error":{"attributes":{"code":"503","text":"` +
			`{\"password\":\"` + secret + `\"} ` + strings.Repeat("x", maxDMEContextLength+50) +
			`"}}}]}`,
	}
	err := redactNXAPIError(wrapDMERequestError(http.MethodGet, "sys\n\x1bsecret", restErr))

	var dmeErr *DMEError
	if !errors.As(err, &dmeErr) {
		t.Fatalf("error=%v does not retain DMEError", err)
	}
	var gotRESTError *configtransport.RESTError
	if !errors.As(err, &gotRESTError) {
		t.Fatalf("error chain did not retain typed RESTError")
	}
	if gotRESTError == restErr {
		t.Fatal("error chain retained the raw RESTError instead of a sanitized clone")
	}
	if gotRESTError.StatusCode != restErr.StatusCode ||
		gotRESTError.Method != restErr.Method ||
		gotRESTError.Path != restErr.Path {
		t.Fatalf("sanitized REST metadata=%#v, want method/path/status from %#v", gotRESTError, restErr)
	}
	if strings.Contains(gotRESTError.Body, secret) || strings.Contains(gotRESTError.Error(), secret) {
		t.Fatalf("typed RESTError leaked credential through errors.As: %#v", gotRESTError)
	}
	if dmeErr.Category != DMEErrorRetryable || dmeErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("DMEError=%#v", dmeErr)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(dmeErr.Context, secret) {
		t.Fatalf("DME error leaked credential: %v", err)
	}
	if !strings.Contains(err.Error(), "***REDACTED***") {
		t.Fatalf("DME error omitted redaction marker: %v", err)
	}
	if len([]rune(dmeErr.Context)) > maxDMEContextLength {
		t.Fatalf("context length=%d, max=%d", len([]rune(dmeErr.Context)), maxDMEContextLength)
	}
	if strings.ContainsAny(dmeErr.DN, "\n\x1b") {
		t.Fatalf("DN contains control characters: %q", dmeErr.DN)
	}
}

func TestNewNXAPIClientHonorsTLSDefaultPort(t *testing.T) {
	c, err := newNXAPIClient(&ciskov1.DeviceSpec{
		Driver:  ciskov1.DeviceDriverNXOS,
		Address: "leaf.example",
		TLS: &ciskov1.TLSConfig{
			Enabled:            true,
			InsecureSkipVerify: true,
		},
	})
	if err != nil {
		t.Fatalf("newNXAPIClient: %v", err)
	}
	if c.rootURL != "https://leaf.example:443" {
		t.Fatalf("rootURL=%q, want HTTPS default port", c.rootURL)
	}
	if c.baseURL != "https://leaf.example:443/ins" {
		t.Fatalf("baseURL=%q, want HTTPS /ins endpoint", c.baseURL)
	}

	c, err = newNXAPIClient(&ciskov1.DeviceSpec{
		Driver:  ciskov1.DeviceDriverNXOS,
		Address: "leaf.example",
	})
	if err != nil {
		t.Fatalf("newNXAPIClient no TLS: %v", err)
	}
	if c.rootURL != "http://leaf.example:80" {
		t.Fatalf("rootURL=%q, want HTTP default port", c.rootURL)
	}
}

func TestParseAppList(t *testing.T) {
	apps := parseAppList(`
App id                                   State
---------------------------------------------------------
cvk0000_0123456789abcdef0123456789abcdef RUNNING
guestshell+                              ACTIVATED
`)
	if len(apps) != 2 {
		t.Fatalf("len(apps)=%d", len(apps))
	}
	if apps[0].ID != "cvk0000_0123456789abcdef0123456789abcdef" || apps[0].State != "RUNNING" {
		t.Fatalf("unexpected first app: %#v", apps[0])
	}
}

func TestParseAppDetailLabels(t *testing.T) {
	app := parseAppDetail(`
App id                 : cvk0000_0123456789abcdef0123456789abcdef
State                  : RUNNING
Package                : bootflash:/hello.tar
IPv4 address           : 10.0.2.10
Docker Run Options     : --label io.kubernetes.pod.name=hello
Docker Run Options     : --label io.kubernetes.pod.namespace=default
Docker Run Options     : --label io.kubernetes.pod.uid=01234567-89ab-cdef-0123-456789abcdef
Docker Run Options     : --label io.kubernetes.container.name=hello
`)
	if app.State != "RUNNING" || app.IPv4 != "10.0.2.10" || app.ContainerName != "hello" {
		t.Fatalf("unexpected app detail: %#v", app)
	}
}

func TestConfigureSignVerificationDisable(t *testing.T) {
	var got nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"ins_api":{"outputs":{"output":{"input":"configure terminal ; app-hosting signed-verification disable","code":"200","msg":"Success","body":""}}}}`))
	}))
	defer server.Close()

	driver := &NXOSDriver{
		client: &nxapiClient{
			baseURL: server.URL,
			client:  server.Client(),
		},
	}

	if err := driver.ConfigureSignVerification(context.Background(), false); err != nil {
		t.Fatalf("ConfigureSignVerification: %v", err)
	}
	if got.InsAPI.Type != "cli_conf" {
		t.Fatalf("type=%q", got.InsAPI.Type)
	}
	if got.InsAPI.Input != "configure terminal ; app-hosting signed-verification disable" {
		t.Fatalf("input=%q", got.InsAPI.Input)
	}
}
