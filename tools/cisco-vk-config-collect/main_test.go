// Copyright © 2026 Cisco Systems, Inc.
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
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// newDeviceMock returns an httptest server that responds to a handful
// of RESTCONF GETs with hand-crafted payloads in the shape the
// writers decode. Unknown paths return 404 so families whose Fetch
// isn't exercised in a test don't pollute the output.
func newDeviceMock(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := responses[r.URL.Path]; ok {
			_, _ = io.WriteString(w, body)
			return
		}
		http.Error(w, `{"errors":"path not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCollectVLANReshape(t *testing.T) {
	srv := newDeviceMock(t, map[string]string{
		"/restconf/data/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list": `
{"Cisco-IOS-XE-vlan:vlan-list":[{"id":10,"name":"users"},{"id":20,"name":"voice"}]}`,
	})

	outFile := filepath.Join(t.TempDir(), "out.yaml")
	var out, errBuf bytes.Buffer
	addr := strings.TrimPrefix(srv.URL, "http://")
	host, port := splitHostPort(t, addr)

	code := run([]string{
		"--scheme", "http",
		"--address", host,
		"--port", port,
		"--username", "u",
		"--password", "p",
		"--device-name", "edge-01",
		"--families", "vlan",
		"--out", outFile,
	}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	var envelope map[string]any
	if err := yaml.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	iosxe, _ := envelope["iosxe"].(map[string]any)
	devs, _ := iosxe["devices"].([]any)
	if len(devs) != 1 {
		t.Fatalf("devices=%v", devs)
	}
	dev := devs[0].(map[string]any)
	if dev["name"] != "edge-01" {
		t.Errorf("device name=%v", dev["name"])
	}
	cfg := dev["configuration"].(map[string]any)
	vlan := cfg["vlan"].(map[string]any)
	vlans := vlan["vlans"].([]any)
	if len(vlans) != 2 {
		t.Fatalf("projected vlans=%v", vlans)
	}
}

func TestCollectUnknownFamilyRejected(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{
		"--address", "ignored",
		"--username", "u",
		"--families", "not_a_family",
	}, &out, &errBuf)
	if code != exitBadFlags {
		t.Fatalf("exit=%d, want exitBadFlags", code)
	}
	if !strings.Contains(errBuf.String(), "not registered") {
		t.Errorf("stderr missing diagnostic:\n%s", errBuf.String())
	}
}

func TestCollectRequiresAddressAndUsername(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"--families", "vlan"}, &out, &errBuf)
	if code != exitBadFlags {
		t.Fatalf("exit=%d", code)
	}
}

func TestCollectAllFamiliesEnumeratesRegistry(t *testing.T) {
	// Mock returns 404 for every path so Fetch errors for every family;
	// the test verifies the CLI accepts --all and emits a best-effort
	// document (continue-on-family-error is on by default).
	srv := newDeviceMock(t, nil)
	addr := strings.TrimPrefix(srv.URL, "http://")
	host, port := splitHostPort(t, addr)

	var out, errBuf bytes.Buffer
	code := run([]string{
		"--scheme", "http",
		"--address", host,
		"--port", port,
		"--username", "u",
		"--password", "p",
		"--all",
		"--out", "-",
	}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}
	// stdout should contain the envelope even if every family 404s
	// (the device key, name, and host are always present).
	if !strings.Contains(out.String(), "iosxe:") {
		t.Errorf("stdout missing iosxe envelope:\n%s", out.String())
	}
}

func splitHostPort(t *testing.T, addr string) (string, string) {
	t.Helper()
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:]
		}
	}
	t.Fatalf("bad addr: %q", addr)
	return "", ""
}
