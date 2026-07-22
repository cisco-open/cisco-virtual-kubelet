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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

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
