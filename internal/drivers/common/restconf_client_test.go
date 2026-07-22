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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	siruplogrus "github.com/sirupsen/logrus"
	vklog "github.com/virtual-kubelet/virtual-kubelet/log"
	vklogrus "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
)

// Dummy struct for testing marshalling/unmarshalling
type testData struct {
	Name string `json:"name"`
}

func TestRestconfClient_Get(t *testing.T) {
	expectedPath := "/restconf/data/test"
	expectedResponse := `{"name":"test-item"}`

	// 1. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Method, Path, and Auth
		if r.Method != "GET" {
			t.Errorf("Expected GET, got %s", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("Expected path %s, got %s", expectedPath, r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "admin" {
			t.Errorf("Basic Auth failed or missing")
		}

		w.Header().Set("Content-Type", "application/yang-data+json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, expectedResponse)
	}))
	defer server.Close()

	// 2. Initialize Client
	auth := &ClientAuth{
		Username: "admin",
		Password: "admin",
	}
	client := NewClientRestconfClient(server.URL, auth, nil, 5*time.Second)

	// 3. Define local unmarshaller logic
	unmarshalFn := func(data []byte, v any) error {
		return json.Unmarshal(data, v)
	}

	// 4. Execute
	var result testData
	err := client.Get(context.Background(), expectedPath, &result, unmarshalFn)

	// 5. Assert
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.Name != "test-item" {
		t.Errorf("Expected name test-item, got %s", result.Name)
	}
}

func TestRestconfClient_Post(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/yang-data+json" {
			t.Errorf("Missing RESTconf Content-Type header")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	auth := &ClientAuth{
		Username: "admin",
		Password: "admin",
	}
	client := NewClientRestconfClient(server.URL, auth, nil, 5*time.Second)

	marshalFn := func(v any) ([]byte, error) {
		return json.Marshal(v)
	}

	payload := testData{Name: "new-item"}
	err := client.Post(context.Background(), "/restconf/data", payload, marshalFn)

	if err != nil {
		t.Errorf("Post failed: %v", err)
	}
}

func TestRestconfClient_Delete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("Expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	auth := &ClientAuth{
		Username: "admin",
		Password: "admin",
	}
	client := NewClientRestconfClient(server.URL, auth, nil, 5*time.Second)

	err := client.Delete(context.Background(), "/restconf/data/item")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}
}

func TestRestconfClient_HttpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 400
	}))
	defer server.Close()

	auth := &ClientAuth{
		Username: "admin",
		Password: "admin",
	}
	client := NewClientRestconfClient(server.URL, auth, nil, 5*time.Second)

	err := client.Get(context.Background(), "/bad", nil, nil)
	if err == nil {
		t.Error("Expected error for 400 status code, got nil")
	}
}

func TestRestconfClientErrorOmitsResponseSecretsAndPreservesTag(t *testing.T) {
	const sentinel = "RESTCONF_SECRET_SENTINEL_DO_NOT_EXPOSE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, `{"ietf-restconf:errors":{"error":[{"error-tag":"data-exists","error-message":"echoed %s"},{"error-tag":"%s"}]}}`, sentinel, sentinel)
	}))
	defer server.Close()

	client := NewClientRestconfClient(server.URL, &ClientAuth{}, nil, 5*time.Second)
	err := client.Post(context.Background(), "/restconf/data", testData{Name: sentinel}, json.Marshal)
	if err == nil {
		t.Fatal("Post succeeded, want RESTCONF error")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("RESTCONF error exposed response Secret: %v", err)
	}
	if !strings.Contains(err.Error(), "409 Conflict") || !HasRESTCONFErrorTag(err, "data-exists") {
		t.Fatalf("RESTCONF error lost safe status/tag metadata: %v", err)
	}
}

func TestRestconfClientDecodeErrorOmitsResponseBody(t *testing.T) {
	const sentinel = "RESTCONF_DECODE_SENTINEL_DO_NOT_EXPOSE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"line-run-opts":%q}`, sentinel)
	}))
	defer server.Close()

	client := NewClientRestconfClient(server.URL, &ClientAuth{}, nil, 5*time.Second)
	err := client.Get(context.Background(), "/restconf/data", &testData{}, func(data []byte, value any) error {
		return fmt.Errorf("failed to decode echoed body %s", data)
	})
	if err == nil || !strings.Contains(err.Error(), "response body omitted") {
		t.Fatalf("Get err=%v, want sanitized decode failure", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("decode error exposed response body: %v", err)
	}
}

func TestRestconfClientIgnoresRemoteStatusReason(t *testing.T) {
	const sentinel = "REMOTE_STATUS_SENTINEL_DO_NOT_EXPOSE"
	client := &RestconfClient{
		BaseURL: "https://device.example",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 " + sentinel,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		})},
	}

	err := client.Get(context.Background(), "/restconf/data", nil, nil)
	if err == nil {
		t.Fatal("Get succeeded, want error")
	}
	if strings.Contains(err.Error(), sentinel) || err.Error() != "request failed with status 400 Bad Request" {
		t.Fatalf("RESTCONF error used remote-controlled status reason: %v", err)
	}
}

func TestRestconfClientRejectsRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/unexpected-target", http.StatusFound)
		case "/unexpected-target":
			redirectedRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClientRestconfClient(server.URL, &ClientAuth{Username: "admin", Password: "password"}, nil, 5*time.Second)
	err := client.Post(context.Background(), "/redirect", testData{Name: "new-item"}, json.Marshal)
	if err == nil {
		t.Fatal("Post followed redirect and succeeded, want redirect error")
	}
	if !IsRESTCONFStatus(err, http.StatusFound) {
		t.Fatalf("Post err=%v, want typed 302 status", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("RESTCONF client followed redirect %d time(s)", got)
	}
}

func TestRestconfClientAcceptsOnly2xxStatuses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{name: "switching protocols", statusCode: http.StatusSwitchingProtocols, wantError: true},
		{name: "informational extension", statusCode: 199, wantError: true},
		{name: "ok", statusCode: http.StatusOK},
		{name: "successful extension", statusCode: 299},
		{name: "multiple choices", statusCode: http.StatusMultipleChoices, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &RestconfClient{
				BaseURL: "https://device.example",
				HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: tt.statusCode,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				})},
			}

			err := client.Get(context.Background(), "/restconf/data", nil, nil)
			if tt.wantError {
				if err == nil {
					t.Fatalf("Get status %d succeeded, want error", tt.statusCode)
				}
				if !IsRESTCONFStatus(err, tt.statusCode) {
					t.Fatalf("Get err=%v, want typed status %d", err, tt.statusCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get status %d failed: %v", tt.statusCode, err)
			}
		})
	}
}

func TestRestconfClientRejectsOversizedSuccessResponse(t *testing.T) {
	const sentinel = "RESTCONF_OVERSIZE_SENTINEL_DO_NOT_EXPOSE"
	client := &RestconfClient{
		BaseURL: "https://device.example",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			body := io.MultiReader(
				io.LimitReader(zeroReader{}, maxRESTCONFResponseBytes),
				strings.NewReader(sentinel),
			)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(body),
			}, nil
		})},
	}

	err := client.Get(context.Background(), "/restconf/data", &testData{}, json.Unmarshal)
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("Get err=%v, want response-size rejection", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("response-size error exposed body content: %v", err)
	}
}

func TestRESTCONFErrorTagsRequireStandardEnvelopeFieldAndValue(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "prefixed standard envelope",
			body: `{"ietf-restconf:errors":{"error":[{"error-tag":"operation-failed"},{"error-tag":"data-exists"},{"error-tag":"operation-failed"}]}}`,
			want: []string{"data-exists", "operation-failed"},
		},
		{
			name: "unprefixed standard envelope",
			body: `{"errors":{"error":[{"error-tag":"invalid-value"}]}}`,
			want: []string{"invalid-value"},
		},
		{
			name: "nested lookalike is ignored",
			body: `{"echo":{"error-tag":"data-exists"}}`,
		},
		{
			name: "vendor envelope is ignored",
			body: `{"vendor:errors":{"error":[{"error-tag":"data-exists"}]}}`,
		},
		{
			name: "suffix field is ignored",
			body: `{"ietf-restconf:errors":{"error":[{"not-error-tag":"data-exists"}]}}`,
		},
		{
			name: "nonstandard tag is ignored",
			body: `{"ietf-restconf:errors":{"error":[{"error-tag":"SECRET_SENTINEL"}]}}`,
		},
		{
			name: "malformed envelope is ignored",
			body: `{"ietf-restconf:errors":{"error":{"error-tag":"data-exists"}}}`,
		},
		{
			name: "malformed document is ignored",
			body: `{"ietf-restconf:errors":`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := restconfErrorTags([]byte(tt.body)); !slices.Equal(got, tt.want) {
				t.Fatalf("restconfErrorTags()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestRESTCONFErrorHelpersHandleWrappedAndTypedNilErrors(t *testing.T) {
	restErr := &RESTCONFError{
		StatusCode: http.StatusConflict,
		Status:     "409 Conflict",
		ErrorTags:  []string{"data-exists"},
	}
	wrapped := fmt.Errorf("create app config: %w", restErr)
	if !IsRESTCONFStatus(wrapped, http.StatusConflict) {
		t.Fatal("IsRESTCONFStatus did not inspect wrapped RESTCONF error")
	}
	if !HasRESTCONFErrorTag(wrapped, "data-exists") {
		t.Fatal("HasRESTCONFErrorTag did not inspect wrapped RESTCONF error")
	}
	if IsRESTCONFStatus(wrapped, http.StatusBadRequest) || HasRESTCONFErrorTag(wrapped, "invalid-value") {
		t.Fatal("RESTCONF helpers matched absent status or tag")
	}

	var typedNil *RESTCONFError
	var err error = typedNil
	if IsRESTCONFStatus(err, http.StatusConflict) || HasRESTCONFErrorTag(err, "data-exists") {
		t.Fatal("RESTCONF helpers matched a typed-nil RESTCONF error")
	}
	if IsRESTCONFStatus(errors.New("409 Conflict"), http.StatusConflict) || HasRESTCONFErrorTag(errors.New("data-exists"), "data-exists") {
		t.Fatal("RESTCONF helpers matched untyped error text")
	}
}

func TestRestconfClientDebugLogsOmitBodies(t *testing.T) {
	const sentinel = "RESTCONF_BODY_SENTINEL_DO_NOT_LOG"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"name":%q}`, sentinel)
			return
		}
		var posted testData
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Errorf("decode request body: %v", err)
		} else if posted.Name != sentinel {
			t.Errorf("request body name=%q, want sentinel", posted.Name)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	var logs bytes.Buffer
	backend := siruplogrus.New()
	backend.SetOutput(&logs)
	backend.SetLevel(siruplogrus.DebugLevel)
	ctx := vklog.WithLogger(context.Background(), vklogrus.FromLogrus(siruplogrus.NewEntry(backend)))
	client := NewClientRestconfClient(server.URL, &ClientAuth{}, nil, 5*time.Second)

	if err := client.Post(ctx, "/restconf/data", testData{Name: sentinel}, json.Marshal); err != nil {
		t.Fatalf("Post: %v", err)
	}
	var result testData
	if err := client.Get(ctx, "/restconf/data", &result, func(data []byte, value any) error {
		return json.Unmarshal(data, value)
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.Name != sentinel {
		t.Fatalf("Get result=%q, want %q", result.Name, sentinel)
	}
	if strings.Contains(logs.String(), sentinel) {
		t.Fatalf("RESTCONF debug logs exposed request or response body: %s", logs.String())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
