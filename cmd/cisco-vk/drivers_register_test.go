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

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
)

func TestCompiledDriverRegistryHasSupportedAppHostingPlatforms(t *testing.T) {
	for _, kind := range []ciskov1.DeviceDriver{
		ciskov1.DeviceDriverXE,
		ciskov1.DeviceDriverNXOS,
		ciskov1.DeviceDriverFAKE,
	} {
		if !drivers.Registered(kind) {
			t.Fatalf("app-hosting driver %s is not compiled into cisco-vk; registered=%v", kind, drivers.RegisteredKinds())
		}
	}
}

func TestCompiledConfigDriverRegistryHasDeviceCentricPlatforms(t *testing.T) {
	for _, kind := range []ciskov1.DeviceDriver{
		ciskov1.DeviceDriverXE,
		ciskov1.DeviceDriverNXOS,
	} {
		if !drivers.ConfigDriverRegistered(kind) {
			t.Fatalf("config driver %s is not compiled into cisco-vk; registered=%v", kind, drivers.RegisteredConfigDriverKinds())
		}
	}
}

func TestCompiledNXOSDriverStartsWithReadOnlyNXAPIProbe(t *testing.T) {
	const (
		username = "nxos-registry-test"
		password = "nxos-registry-test-credential"
	)
	type capturedRequest struct {
		Type  string
		Input string
	}

	var (
		mu          sync.Mutex
		requests    []capturedRequest
		handlerErrs []string
		authOK      = true
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			InsAPI struct {
				Type  string `json:"type"`
				Input string `json:"input"`
			} `json:"ins_api"`
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost {
			handlerErrs = append(handlerErrs, "NX-API probe did not use POST")
		}
		if r.URL.Path != "/ins" {
			handlerErrs = append(handlerErrs, "NX-API probe did not target /ins")
		}
		gotUser, gotPassword, ok := r.BasicAuth()
		if !ok || gotUser != username || gotPassword != password {
			authOK = false
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			handlerErrs = append(handlerErrs, "NX-API probe body was not valid JSON")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests = append(requests, capturedRequest{
			Type:  payload.InsAPI.Type,
			Input: payload.InsAPI.Input,
		})

		body := "NAME: chassis, PID: N9K-C9300v, SN: KIND-SMOKE"
		if payload.InsAPI.Input == "show version" {
			body = "Device name: nxos-kind-smoke\nNXOS: version 10.3(9)\n"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"ins_api": map[string]any{
				"outputs": map[string]any{
					"output": map[string]any{
						"input": payload.InsAPI.Input,
						"code":  "200",
						"msg":   "Success",
						"body":  body,
					},
				},
			},
		}); err != nil {
			handlerErrs = append(handlerErrs, "NX-API test server could not encode its response")
		}
	}))
	defer server.Close()

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse NX-API test endpoint: %v", err)
	}
	host, portText, err := net.SplitHostPort(endpoint.Host)
	if err != nil {
		t.Fatalf("split NX-API test endpoint: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse NX-API test port: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	driver, err := drivers.NewDriver(ctx, &ciskov1.DeviceSpec{
		Driver:   ciskov1.DeviceDriverNXOS,
		Address:  host,
		Port:     port,
		Username: username,
		Password: password,
		TLS:      &ciskov1.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatalf("construct compiled NX-OS driver: %v", err)
	}
	if driver == nil {
		t.Fatal("compiled NX-OS driver constructor returned nil")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(handlerErrs) > 0 {
		t.Fatalf("NX-API probe contract errors: %v", handlerErrs)
	}
	if !authOK {
		t.Fatal("NX-API probe did not use the configured Basic authentication")
	}
	want := []capturedRequest{
		{Type: "cli_show_ascii", Input: "show version"},
		{Type: "cli_show_ascii", Input: "show inventory"},
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("NX-API startup requests=%v, want read-only probes %v", requests, want)
	}
}
