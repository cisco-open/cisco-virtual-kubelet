// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fakeRateLimiter struct {
	calls int
	err   error
}

func (f *fakeRateLimiter) Wait(context.Context) error {
	f.calls++
	return f.err
}

func TestRESTClientDoBuildsRequest(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotHeader, gotDefault string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Get("X-Request")
		gotDefault = r.Header.Get("X-Default")
		user, pass, _ := r.BasicAuth()
		gotAuth = user + ":" + pass
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	limiter := &fakeRateLimiter{}
	client, err := NewRESTClient(server.URL+"/api", RESTClientOptions{
		HTTPClient: server.Client(),
		Auth:       RESTAuth{Username: "admin", Password: "pw"},
		Headers:    map[string]string{"X-Default": "default"},
	})
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}
	client.RateLimiter = limiter
	body, err := client.Do(context.Background(), RESTRequest{
		Method: http.MethodPost,
		Path:   "/mo/sys.json",
		Query:  url.Values{"target-subtree-class": []string{"l1PhysIf"}},
		Body:   []byte(`{"payload":true}`),
		Headers: map[string]string{
			"X-Request": "request",
		},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body=%s", body)
	}
	if gotPath != "/api/mo/sys.json" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotQuery != "target-subtree-class=l1PhysIf" {
		t.Fatalf("query=%q", gotQuery)
	}
	if gotAuth != "admin:pw" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotHeader != "request" {
		t.Fatalf("header=%q", gotHeader)
	}
	if gotDefault != "default" {
		t.Fatalf("default header=%q", gotDefault)
	}
	if limiter.calls != 1 {
		t.Fatalf("rate limiter calls=%d", limiter.calls)
	}
}

func TestRESTClientDoRawReturnsResponseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Trace", "abc")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`accepted`))
	}))
	defer server.Close()

	client, err := NewRESTClient(server.URL, RESTClientOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}
	resp, err := client.DoRaw(context.Background(), RESTRequest{Path: "task/1"})
	if err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Trace") != "abc" || string(resp.Body) != "accepted" {
		t.Fatalf("resp=%#v", resp)
	}
}

func TestRESTClientRedactsErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"password":"cleartext"}`))
	}))
	defer server.Close()

	client, err := NewRESTClient(server.URL, RESTClientOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}
	_, err = client.Do(context.Background(), RESTRequest{Path: "/bad"})
	if err == nil {
		t.Fatal("Do error=nil, want HTTP error")
	}
	if strings.Contains(err.Error(), "cleartext") || !strings.Contains(err.Error(), "***REDACTED***") {
		t.Fatalf("error was not redacted: %v", err)
	}
}

func TestNewRESTClientRejectsInvalidBaseURL(t *testing.T) {
	if _, err := NewRESTClient("/relative", RESTClientOptions{}); err == nil {
		t.Fatal("NewRESTClient accepted relative URL")
	}
}
