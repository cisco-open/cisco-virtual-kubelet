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

package ap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestIOxClientAddresses(t *testing.T) {
	for _, tt := range []struct {
		address string
		want    string
	}{
		{"ap.example.test", "ap.example.test:8443"},
		{"192.0.2.1", "192.0.2.1:8443"},
		{"2001:db8::1", "[2001:db8::1]:8443"},
		{"[2001:db8::1]", "[2001:db8::1]:8443"},
	} {
		t.Run(tt.address, func(t *testing.T) {
			client, err := newIOxClient(&v1alpha1.DeviceSpec{Address: tt.address})
			if err != nil {
				t.Fatal(err)
			}
			want := "https://" + tt.want + ioxAPIPrefix
			if client.baseURL != want {
				t.Fatalf("URL = %q, want %q", client.baseURL, want)
			}
		})
	}
	for _, address := range []string{"", " ", "https://ap.example.test", "ap.example.test:8443", "user@ap.example.test", "ap.example.test/path", "ap.example.test?query", "ap.example.test#fragment", "ap.example.test\\path", "ap.example.test\n.evil", "[not-ipv6]", "[2001:db8::1"} {
		t.Run("invalid-"+address, func(t *testing.T) {
			_, err := newIOxClient(&v1alpha1.DeviceSpec{Address: address})
			if err == nil {
				t.Fatal("accepted a URL, port, or malformed address")
			}
		})
	}
}

func TestIOxClientUsesDeviceCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ioxAPIPrefix+"/tokenservice" {
			writeJSON(t, w, map[string]any{"token": map[string]string{"id": "token"}})
			return
		}
		_, _ = io.WriteString(w, "[]")
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newIOxClient(&v1alpha1.DeviceSpec{Address: host, Port: port, TLS: &v1alpha1.TLSConfig{CAFile: caFile}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.listApps(context.Background()); err != nil {
		t.Fatalf("private CA was not trusted: %v", err)
	}
	transport := client.http.Transport.(*http.Transport)
	if transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("client downgraded TLS verification")
	}
}

func TestIOxClientRenewsOnlyExpiredAuthenticationAndReplaysUpload(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"expired", http.StatusUnauthorized, "invalid / expired token", false},
		{"expired-no-spaces", http.StatusUnauthorized, "invalid/expired token", false},
		{"other-unauthorized", http.StatusUnauthorized, "permission denied", true},
		{"forbidden", http.StatusForbidden, "invalid / expired token", true},
		{"server-error", http.StatusInternalServerError, "invalid / expired token", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var auth, uploads atomic.Int32
			payload := []byte("package archive bytes")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == ioxAPIPrefix+"/tokenservice" {
					id := auth.Add(1)
					writeJSON(t, w, map[string]any{"token": map[string]string{"id": fmt.Sprintf("token-%d", id)}})
					return
				}
				uploads.Add(1)
				got, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(got, payload) || r.Header.Get("X-Connector-Id") != "app-id" {
					t.Error("upload body or connector ID was lost on authentication retry")
				}
				if r.Header.Get("X-Token-Id") == "token-1" {
					http.Error(w, tt.body, tt.status)
					return
				}
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()
			client := testIOxClient(server)
			err := client.doWithHeaders(context.Background(), http.MethodPost, "apps", "application/octet-stream", payload, http.Header{"X-Connector-Id": {"app-id"}}, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			want := int32(2)
			if tt.wantErr {
				want = 1
			}
			if auth.Load() != want || uploads.Load() != want {
				t.Fatalf("authentication/upload calls = %d/%d, want %d/%d", auth.Load(), uploads.Load(), want, want)
			}
		})
	}
}

func TestIOxClientLimitsAuthenticationRenewal(t *testing.T) {
	var auth, attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ioxAPIPrefix+"/tokenservice" {
			auth.Add(1)
			writeJSON(t, w, map[string]any{"token": map[string]string{"id": "rejected-token"}})
			return
		}
		attempts.Add(1)
		http.Error(w, "invalid / expired token", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := testIOxClient(server)
	_, err := client.listApps(context.Background())
	if !isHTTPStatus(err, http.StatusUnauthorized) || auth.Load() != 2 || attempts.Load() != 2 {
		t.Fatalf("renewal was not bounded: error %v, authentication %d, attempts %d", err, auth.Load(), attempts.Load())
	}
}

func TestIOxClientAuthenticationSingleFlightAndCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var auth atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ioxAPIPrefix+"/tokenservice" {
			if auth.Add(1) == 1 {
				close(started)
			}
			<-release
			writeJSON(t, w, map[string]any{"token": map[string]string{"id": "shared-token"}})
			return
		}
		_, _ = io.WriteString(w, "[]")
	}))
	defer server.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	client := testIOxClient(server)
	first := make(chan error, 1)
	go func() {
		_, err := client.listApps(context.Background())
		first <- err
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cancelled := make(chan error, 1)
	go func() {
		_, err := client.listApps(ctx)
		cancelled <- err
	}()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting authentication error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authentication waiter ignored its deadline")
	}

	const workers = 10
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			if _, err := client.listApps(context.Background()); err != nil {
				t.Errorf("concurrent inventory: %v", err)
			}
		})
	}
	unblock()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if auth.Load() != 1 {
		t.Fatalf("authentication calls = %d, want one", auth.Load())
	}
	client.discardToken("obsolete-token")
	if _, err := client.listApps(context.Background()); err != nil || auth.Load() != 1 {
		t.Fatalf("a stale rejection discarded the current token: %v", err)
	}
}

func TestIOxClientRejectsRedirectsWithoutLeakingCredentials(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		t.Error("followed an AP redirect to a different endpoint")
	}))
	defer target.Close()
	for _, token := range []string{"", "token-for-device"} {
		t.Run("cached-token-"+strconv.FormatBool(token != ""), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			host, portText, _ := net.SplitHostPort(u.Host)
			port, _ := strconv.Atoi(portText)
			client, err := newIOxClient(&v1alpha1.DeviceSpec{Address: host, Port: port, Username: "user", Password: "secret", TLS: &v1alpha1.TLSConfig{InsecureSkipVerify: true}})
			if err != nil {
				t.Fatal(err)
			}
			client.token = token
			if _, err := client.listApps(context.Background()); err == nil {
				t.Fatal("accepted redirected authentication/inventory")
			}
		})
	}
	if received.Load() != 0 {
		t.Fatal("redirect target received credentials or tokens")
	}
}

func TestIOxClientDoesNotExposeResponseSecrets(t *testing.T) {
	const secret = "a-device-credential-never-logged"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, secret, http.StatusForbidden)
	}))
	defer server.Close()
	client := testIOxClient(server)
	client.token = "token"
	_, err := client.app(context.Background(), "hello")
	if !isHTTPStatus(err, http.StatusForbidden) {
		t.Fatalf("error lost its HTTP status: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error exposed the response body")
	}
}

func TestIOxClientRejectsInvalidInventoryAndStatusResponses(t *testing.T) {
	for _, body := range []string{"", "null", "{}", `{"apps":null}`, `{"other":[]}`, `{"apps":"invalid"}`, `not JSON`, `[null]`, `[{}]`, `[{"id":"same"},{"id":"same"}]`} {
		t.Run("list-"+body, func(t *testing.T) {
			client, cleanup := responseClient(t, body)
			defer cleanup()
			if _, err := client.listApps(context.Background()); err == nil {
				t.Fatal("accepted ambiguous/malformed inventory as an empty device")
			}
		})
	}
	for _, body := range []string{"[]", `{"apps":[]}`, `{"items":[]}`, `[{"id":"hello","state":"RUNNING"}]`} {
		t.Run("valid-list-"+body, func(t *testing.T) {
			client, cleanup := responseClient(t, body)
			defer cleanup()
			if _, err := client.listApps(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, body := range []string{"", "null", "{}", `{"id":"hello"}`, `{"id":"other","state":"RUNNING"}`, `{"id":"hello","state":" "}`} {
		t.Run("app-"+body, func(t *testing.T) {
			client, cleanup := responseClient(t, body)
			defer cleanup()
			if _, err := client.app(context.Background(), "hello"); err == nil {
				t.Fatal("accepted ambiguous/malformed application state")
			}
		})
	}
	for _, body := range []string{`{"state":"RUNNING"}`, `{"id":"hello","state":"DEPLOYED"}`} {
		t.Run("valid-app-"+body, func(t *testing.T) {
			client, cleanup := responseClient(t, body)
			defer cleanup()
			app, err := client.app(context.Background(), "hello")
			if err != nil || app.ID != "hello" {
				t.Fatalf("valid app = %#v, %v", app, err)
			}
		})
	}
}

func TestIOxClientBoundsResponsesAndUploads(t *testing.T) {
	client, cleanup := responseClient(t, strings.Repeat("x", maxAPIResponseSize+1))
	defer cleanup()
	if _, err := client.listApps(context.Background()); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized response error = %v", err)
	}
	for _, packageData := range [][]byte{nil, make([]byte, maxPackageSize+1)} {
		// No HTTP client is needed: invalid packages must fail before network I/O.
		client := &ioxClient{}
		if err := client.install(context.Background(), "cvk0000_123e4567e89b12d3a456426614174000", packageData); err == nil || !strings.Contains(err.Error(), "package is empty or exceeds") {
			t.Fatalf("empty/oversized package error = %v", err)
		}
	}
}

func testIOxClient(server *httptest.Server) *ioxClient {
	return &ioxClient{baseURL: server.URL + ioxAPIPrefix, username: "user", password: "password", http: server.Client()}
}

func responseClient(t *testing.T, body string) (*ioxClient, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	client := testIOxClient(server)
	client.token = "token"
	return client, server.Close
}
