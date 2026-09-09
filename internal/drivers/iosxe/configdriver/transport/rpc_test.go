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

package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvokeRPCUsesOperationsRoot(t *testing.T) {
	const payload = `{"Cisco-IOS-XE-install-rpc:input":{"uuid":"00000000-0000-4000-8000-000000000001","one-shot":false,"path":"flash:image.bin"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/restconf/operations/Cisco-IOS-XE-install-rpc:install" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got, want := r.Header.Get("Content-Type"), "application/yang-data+json"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "operator" || password != "secret" {
			t.Errorf("BasicAuth = (%q, %q, %t)", username, password, ok)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if string(body) != payload {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	base, err := NewRESTCONF(RESTCONFConfig{
		BaseURL:    server.URL + "/restconf/data",
		HTTPClient: server.Client(),
		Username:   "operator",
		Password:   "secret",
	})
	if err != nil {
		t.Fatalf("NewRESTCONF: %v", err)
	}
	rpc, ok := base.(interface {
		InvokeRPC(context.Context, string, []byte) ([]byte, error)
	})
	if !ok {
		t.Fatal("RESTCONF transport does not implement InvokeRPC")
	}
	if _, err := rpc.InvokeRPC(context.Background(), "/operations/Cisco-IOS-XE-install-rpc:install", []byte(payload)); err != nil {
		t.Fatalf("InvokeRPC: %v", err)
	}
}

func TestInvokeRPCRejectsNonOperationPaths(t *testing.T) {
	r := &restconfTransport{}
	for _, path := range []string{
		"/Cisco-IOS-XE-install-rpc:install",
		"/operations/",
		"/operations//install",
		"/operations/install?input=unsafe",
		"/operations/install#fragment",
		"/operations/install\\suffix",
		"/operations/install\nother",
	} {
		if _, err := r.InvokeRPC(context.Background(), path, nil); err == nil {
			t.Errorf("InvokeRPC(%q) succeeded", path)
		}
	}
}
