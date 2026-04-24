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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// splitHostPort pulls the host/port parts out of an httptest.Server
// URL. Used across every integration test in this file.
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

// newMockDevice returns an httptest server that responds to the
// supplied path→body map with 200+JSON and returns 404 for anything
// else. Mirrors the device-mock pattern the other tools use.
func newMockDevice(t *testing.T, responses map[string]string) *httptest.Server {
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

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cr.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// ---------------------------------------------------------------
// CR loader (crs.go) tests
// ---------------------------------------------------------------

const matchingCR = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  source:
    inline:
      vlan:
        vlans:
          - {id: 10, name: users}
          - {id: 20, name: voice}
`

const wrongDeviceCR = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: other, namespace: network}
spec:
  deviceRef: {name: other-device}
  managedFamilies: [vrf]
  source: {inline: {}}
`

const configMapSourceCR = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01-bulk, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [system]
  source:
    configMapRef: {name: edge-01-data, key: data.nac.yaml}
`

const foreignKindDoc = `
apiVersion: apps/v1
kind: Deployment
metadata: {name: ignored}
`

func TestLoadCRsFiltersByDeviceName(t *testing.T) {
	body := matchingCR + "\n---\n" + wrongDeviceCR + "\n---\n" + foreignKindDoc
	p := writeTemp(t, body)
	crs, err := loadCRsFromFiles([]string{p}, "edge-01")
	if err != nil {
		t.Fatalf("loadCRsFromFiles: %v", err)
	}
	if len(crs) != 1 {
		t.Fatalf("got %d CRs, want 1 (only edge-01 matches)", len(crs))
	}
	if crs[0].FullName != "network/edge-01" {
		t.Errorf("FullName=%q", crs[0].FullName)
	}
	if len(crs[0].ManagedFamilies) != 1 || crs[0].ManagedFamilies[0] != "vlan" {
		t.Errorf("managedFamilies=%v", crs[0].ManagedFamilies)
	}
	if crs[0].InlineSource["vlan"] == nil {
		t.Errorf("inline source missing vlan body")
	}
}

func TestLoadCRsCapturesConfigMapRef(t *testing.T) {
	p := writeTemp(t, configMapSourceCR)
	crs, err := loadCRsFromFiles([]string{p}, "edge-01")
	if err != nil {
		t.Fatalf("loadCRsFromFiles: %v", err)
	}
	if len(crs) != 1 {
		t.Fatalf("got %d CRs, want 1", len(crs))
	}
	if crs[0].SourceViaConfigMap == "" {
		t.Errorf("SourceViaConfigMap empty; want ConfigMap note")
	}
	if crs[0].InlineSource != nil {
		t.Errorf("InlineSource should be nil for configMapRef source")
	}
}

func TestDiscoverCRFilesWalksDirectory(t *testing.T) {
	dir := t.TempDir()
	// YAML we want found.
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(matchingCR), 0o600); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	// Nested YAML we want found.
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.yml"), []byte(wrongDeviceCR), 0o600); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	// Non-YAML we want ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# docs"), 0o600); err != nil {
		t.Fatalf("seed README: %v", err)
	}

	files, err := discoverCRFiles([]string{dir})
	if err != nil {
		t.Fatalf("discoverCRFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files=%v, want 2 YAMLs", files)
	}
}

func TestBuildDriftInputsBuildsClaimerIndex(t *testing.T) {
	crs := []loadedCR{
		{
			FullName: "network/a", DeviceName: "edge-01",
			ManagedFamilies: []string{"vlan", "vrf"},
			InlineSource:    map[string]any{"vlan": map[string]any{}},
		},
		{
			FullName: "network/b", DeviceName: "edge-01",
			ManagedFamilies: []string{"vlan", "system"},
			InlineSource:    map[string]any{"system": map[string]any{"hostname": "edge-01"}},
		},
	}
	inputs := buildDriftInputs("edge-01", crs)
	if got := inputs.claimers["vlan"]; len(got) != 2 {
		t.Errorf("vlan claimers=%v, want both CRs", got)
	}
	if got := inputs.claimers["system"]; len(got) != 1 || got[0] != "network/b" {
		t.Errorf("system claimers=%v, want only b", got)
	}
	if _, ok := inputs.intents["system"]; !ok {
		t.Errorf("system intent missing")
	}
}

// ---------------------------------------------------------------
// Drift engine (drift.go) tests using a mock device
// ---------------------------------------------------------------

const vlanOneEntry = `{"Cisco-IOS-XE-vlan:vlan-list":[{"id":10,"name":"users"}]}`
const vrfOneEntry = `{"Cisco-IOS-XE-native:definition":[{"name":"MGMT","rd":"65000:1"}]}`

func TestComputeReportNoDriftWhenDesiredMatchesObserved(t *testing.T) {
	srv := newMockDevice(t, map[string]string{
		"/restconf/data/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list": vlanOneEntry,
	})
	host, port := splitHostPort(t, strings.TrimPrefix(srv.URL, "http://"))

	p := writeTemp(t, matchingCR) // declares vlan 10 + 20
	files, _ := discoverCRFiles([]string{p})
	crs, _ := loadCRsFromFiles(files, "edge-01")
	inputs := buildDriftInputs("edge-01", crs)

	cli, err := buildTransport(flags{
		scheme: "http", address: host, username: "u", port: atoi(port),
	})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}

	r := computeReport(context.Background(), cli, inputs, nil)

	// Device has vlan 10; intent has vlan 10 AND vlan 20.
	// vlan 10 → no change, vlan 20 → create. So 1 managed-drift entry.
	if len(r.ManagedDrift) != 1 || r.ManagedDrift[0].Family != "vlan" {
		t.Fatalf("ManagedDrift=%+v, want vlan", r.ManagedDrift)
	}
	if r.ManagedDrift[0].OpCount != 1 {
		t.Errorf("opCount=%d, want 1 (just vlan 20 create)", r.ManagedDrift[0].OpCount)
	}
}

// atoi is a tiny decimal-only parser used to turn httptest's
// dynamic port string into the int buildTransport expects. The
// standard library strconv.Atoi would also work; this is here to
// keep the test import list narrow.
func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func TestComputeReportEmitsOrphanWhenDeviceHasUnclaimedFamily(t *testing.T) {
	srv := newMockDevice(t, map[string]string{
		// Device has vrf configured; no CR claims it.
		"/restconf/data/Cisco-IOS-XE-native:native/vrf/definition": vrfOneEntry,
	})
	host, port := splitHostPort(t, strings.TrimPrefix(srv.URL, "http://"))

	// Supply a CR that claims nothing — forces orphan surface.
	emptyCR := `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: empty, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  source: {inline: {}}
`
	p := writeTemp(t, emptyCR)
	files, _ := discoverCRFiles([]string{p})
	crs, _ := loadCRsFromFiles(files, "edge-01")
	inputs := buildDriftInputs("edge-01", crs)

	cli, err := buildTransport(flags{
		scheme: "http", address: host, username: "u", port: atoi(port),
	})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}

	r := computeReport(context.Background(), cli, inputs, nil)

	// vrf should be reported as an orphan.
	found := false
	for _, o := range r.Orphans {
		if o.Family == "vrf" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected vrf in orphans, got %+v", r.Orphans)
	}
}

func TestComputeReportIgnoredFamilySkippedBothDimensions(t *testing.T) {
	srv := newMockDevice(t, map[string]string{
		"/restconf/data/Cisco-IOS-XE-native:native/vrf/definition": vrfOneEntry,
	})
	host, port := splitHostPort(t, strings.TrimPrefix(srv.URL, "http://"))

	emptyCR := `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: empty, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  source: {inline: {}}
`
	p := writeTemp(t, emptyCR)
	files, _ := discoverCRFiles([]string{p})
	crs, _ := loadCRsFromFiles(files, "edge-01")
	inputs := buildDriftInputs("edge-01", crs)

	cli, _ := buildTransport(flags{scheme: "http", address: host, username: "u", port: atoi(port)})
	r := computeReport(context.Background(), cli, inputs,
		map[string]struct{}{"vrf": {}})

	for _, o := range r.Orphans {
		if o.Family == "vrf" {
			t.Fatalf("ignored family 'vrf' leaked into orphans")
		}
	}
}

// ---------------------------------------------------------------
// CLI dispatch + exit-code tests
// ---------------------------------------------------------------

func TestRunMissingRequiredFlagsReturnsBadFlags(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{}, &out, &errBuf)
	if code != exitBadFlags {
		t.Fatalf("exit=%d, want exitBadFlags", code)
	}
	if !strings.Contains(errBuf.String(), "--address") {
		t.Errorf("stderr missing --address diagnostic:\n%s", errBuf.String())
	}
}

func TestRunInvalidModeRejected(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{
		"--address", "x", "--username", "u", "--device-name", "d",
		"--mode", "nonsense", "path.yaml",
	}, &out, &errBuf)
	if code != exitBadFlags {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(errBuf.String(), "invalid --mode") {
		t.Errorf("stderr missing mode error:\n%s", errBuf.String())
	}
}

func TestRunExitCode4OnDriftWithFlag(t *testing.T) {
	srv := newMockDevice(t, map[string]string{
		"/restconf/data/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list": vlanOneEntry,
	})
	host, port := splitHostPort(t, strings.TrimPrefix(srv.URL, "http://"))
	p := writeTemp(t, matchingCR) // declares vlans 10 + 20; device has 10

	var out, errBuf bytes.Buffer
	code := run([]string{
		"--scheme", "http",
		"--address", host,
		"--port", port,
		"--username", "u",
		"--password", "p",
		"--device-name", "edge-01",
		"--exit-on-drift",
		p,
	}, &out, &errBuf)

	if code != exitFindings {
		t.Fatalf("exit=%d (stdout=%q stderr=%q), want exitFindings", code, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "vlan") {
		t.Errorf("stdout missing vlan drift line:\n%s", out.String())
	}
}

func TestRunExitCode0OnCleanDrift(t *testing.T) {
	srv := newMockDevice(t, map[string]string{
		// Device has exactly what intent declares.
		"/restconf/data/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list": `{"Cisco-IOS-XE-vlan:vlan-list":[{"id":10,"name":"users"},{"id":20,"name":"voice"}]}`,
	})
	host, port := splitHostPort(t, strings.TrimPrefix(srv.URL, "http://"))
	p := writeTemp(t, matchingCR)

	var out, errBuf bytes.Buffer
	code := run([]string{
		"--scheme", "http",
		"--address", host, "--port", port,
		"--username", "u", "--password", "p",
		"--device-name", "edge-01",
		"--exit-on-drift",
		// Ignore every family the mock doesn't serve so those
		// don't orphan-surface and flip the exit code.
		"--ignore-families", everyFamilyExceptVLAN(),
		p,
	}, &out, &errBuf)

	if code != exitOK {
		t.Fatalf("exit=%d (stdout=%q stderr=%q), want exitOK", code, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "no claimed family has diverged") {
		t.Errorf("stdout missing clean-drift banner:\n%s", out.String())
	}
}

// everyFamilyExceptVLAN returns a comma-separated list of every
// registered family minus vlan. Used by tests that need to isolate
// the vlan path against a mock device that only serves vlan.
func everyFamilyExceptVLAN() string {
	// Small set of likely-present families the mock doesn't serve;
	// any one of them would orphan-surface and flip exit code. We
	// don't need the whole registry — just the ones a fresh device
	// typically has (system).
	return "system,cdp,lldp,clock,banner,logging,snmp_server,aaa,errdisable,spanning_tree,event_manager,ip_community_list,ip_domain,ip_name_server,ip_http,ip_ssh,ip_nat_inside_source"
}

func TestRunJSONOutputIsValidJSON(t *testing.T) {
	srv := newMockDevice(t, nil)
	host, port := splitHostPort(t, strings.TrimPrefix(srv.URL, "http://"))
	p := writeTemp(t, matchingCR)

	var out, errBuf bytes.Buffer
	code := run([]string{
		"--scheme", "http",
		"--address", host, "--port", port,
		"--username", "u", "--password", "p",
		"--device-name", "edge-01",
		"--output", "json",
		p,
	}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}

	var report Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json decode: %v\n%s", err, out.String())
	}
	if report.Device != "edge-01" {
		t.Errorf("device=%q", report.Device)
	}
	if len(report.ManagedFamilies) != 1 || report.ManagedFamilies[0] != "vlan" {
		t.Errorf("managedFamilies=%v", report.ManagedFamilies)
	}
}

func TestFilterReportRespectsMode(t *testing.T) {
	r := Report{
		Device:          "d",
		ManagedFamilies: []string{"a"},
		ManagedDrift:    []FamilyDrift{{Family: "a"}},
		Orphans:         []Orphan{{Family: "b"}},
		Errors:          []FamilyError{{Family: "c"}},
	}

	drift := filterReport(r, modeDrift)
	if len(drift.ManagedDrift) != 1 || len(drift.Orphans) != 0 {
		t.Errorf("modeDrift=%+v", drift)
	}
	if len(drift.Errors) != 1 {
		t.Errorf("modeDrift dropped errors")
	}

	orphans := filterReport(r, modeOrphans)
	if len(orphans.ManagedDrift) != 0 || len(orphans.Orphans) != 1 {
		t.Errorf("modeOrphans=%+v", orphans)
	}

	full := filterReport(r, modeFull)
	if len(full.ManagedDrift) != 1 || len(full.Orphans) != 1 {
		t.Errorf("modeFull=%+v", full)
	}
}
