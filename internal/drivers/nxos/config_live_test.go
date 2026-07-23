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
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	nxoswriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/writers"
)

const (
	liveNXOSReservedVLANAbsentMarker = "CVK_NXOS_RESERVED_VLAN_ABSENT"
	liveNXOSMinMutableVLAN           = 2
	liveNXOSMaxMutableVLAN           = 3967
)

// TestLiveNXOSReservedVLANAbsent is the read-only ownership preflight for a
// reserved lab VLAN. The success marker is intentionally stable so an outer
// workflow can require evidence from this exact NX-API DME transport path
// before it enables any mutating test stage. Live callers must use
// `go test -count=1 -v` so Go's test cache cannot replay stale device state and
// the marker is emitted for the workflow to assert.
func TestLiveNXOSReservedVLANAbsent(t *testing.T) {
	if os.Getenv("RUN_LIVE_NXOS_VLAN_PREFLIGHT") != "1" {
		t.Skip("set RUN_LIVE_NXOS_VLAN_PREFLIGHT=1 to verify a reserved lab VLAN")
	}
	vlanID, err := parseLiveNXOSVLANID(os.Getenv("NXOS_LIVE_RESERVED_VLAN_ID"))
	if err != nil {
		t.Fatalf("NXOS_LIVE_RESERVED_VLAN_ID: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tr := newLiveNXOSConfigTransport(t)
	defer tr.Close()
	nxapiTransport, ok := tr.(*nxapiConfigTransport)
	if !ok {
		t.Fatalf("reserved VLAN preflight: transport type %T is not NX-API", tr)
	}

	reader := nxapiReservedVLANDMEReader{transport: nxapiTransport}
	if err := requireReservedVLANAbsent(ctx, reader, vlanID); err != nil {
		t.Fatalf("reserved VLAN preflight: %v", err)
	}
	t.Logf("%s=%d", liveNXOSReservedVLANAbsentMarker, vlanID)
}

func TestLiveNXOSConfigSmoke(t *testing.T) {
	if os.Getenv("RUN_LIVE_NXOS_CONFIG") != "1" {
		t.Skip("set RUN_LIVE_NXOS_CONFIG=1 to run the live NX-OS config smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tr := newLiveNXOSConfigTransport(t)
	defer tr.Close()

	for _, path := range []string{
		nxosschema.PathSystemHostname,
		nxosschema.PathFeature,
		nxosschema.PathFeatureSet,
		nxosschema.PathVLANBrief,
		nxosschema.PathInterfaceEthernet,
	} {
		raw, err := tr.Fetch(ctx, path)
		if err != nil {
			t.Fatalf("Fetch(%s): %v", path, err)
		}
		t.Logf("Fetch(%s) ok (%d bytes)", path, len(raw))
	}

	if os.Getenv("RUN_LIVE_NXOS_CONFIG_WRITE") != "1" {
		t.Skip("read-only smoke passed; set RUN_LIVE_NXOS_CONFIG_WRITE=1 to exercise apply/verify")
	}
	vlanID := 3903
	if raw := os.Getenv("NXOS_LIVE_VLAN_ID"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("NXOS_LIVE_VLAN_ID: %v", err)
		}
		vlanID = id
	}
	w := nxoswriters.Get(nxosschema.FamilyVLAN)
	observed, err := w.Fetch(ctx, tr)
	if err != nil {
		t.Fatalf("vlan Fetch: %v", err)
	}
	if vlanPresent(observed, vlanID) {
		t.Skipf("VLAN %d already exists; refusing to mutate an existing lab VLAN", vlanID)
	}
	name := fmt.Sprintf("cvk_live_%d", time.Now().Unix()%100000)
	cleanupNeeded := false
	defer func() {
		if !cleanupNeeded {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		current, fetchErr := w.Fetch(cleanupCtx, tr)
		if fetchErr != nil {
			t.Errorf("VLAN %d cleanup precheck: %v", vlanID, fetchErr)
			return
		}
		item, present := vlanByID(current, vlanID)
		if !present {
			t.Logf("VLAN %d cleanup: already absent", vlanID)
			return
		}
		if got := fmt.Sprint(item["name"]); got != name {
			t.Errorf("VLAN %d cleanup refused: marker changed from %q to %q", vlanID, name, got)
			return
		}
		ntr, ok := tr.(*nxapiConfigTransport)
		if !ok {
			t.Errorf("VLAN %d cleanup: transport type %T does not support guarded DME cleanup", vlanID, tr)
			return
		}
		path := fmt.Sprintf("%s/bd-[vlan-%d]", nxosschema.DNBridgeDomain, vlanID)
		if deleteErr := ntr.client.dmeDelete(cleanupCtx, path); deleteErr != nil {
			if _, fallbackErr := ntr.client.conf(cleanupCtx, "configure terminal", fmt.Sprintf("no vlan %d", vlanID)); fallbackErr != nil {
				t.Errorf("VLAN %d cleanup: DME delete failed: %v; CLI fallback failed: %v", vlanID, deleteErr, fallbackErr)
				return
			}
			t.Errorf("VLAN %d cleanup: DME delete failed, CLI fallback succeeded: %v", vlanID, deleteErr)
		}
		cleaned, fetchErr := w.Fetch(cleanupCtx, tr)
		if fetchErr != nil {
			t.Errorf("VLAN %d cleanup verification: %v", vlanID, fetchErr)
			return
		}
		if vlanPresent(cleaned, vlanID) {
			t.Errorf("VLAN %d cleanup verification: VLAN is still present", vlanID)
			return
		}
		t.Logf("VLAN %d cleanup verified", vlanID)
	}()
	observed, err = w.Fetch(ctx, tr)
	if err != nil {
		t.Fatalf("vlan mutation precheck Fetch: %v", err)
	}
	if vlanPresent(observed, vlanID) {
		t.Fatalf("VLAN %d appeared after the initial precheck; refusing to mutate it", vlanID)
	}
	ops, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": vlanID, "name": name}}}, observed)
	if err != nil {
		t.Fatalf("vlan Diff: %v", err)
	}
	if len(ops) == 0 {
		t.Fatalf("vlan Diff produced no ops for absent VLAN %d", vlanID)
	}
	cleanupNeeded = true
	if err := w.Apply(ctx, tr, ops); err != nil {
		t.Fatalf("vlan Apply: %v", err)
	}
	verify, err := w.Fetch(ctx, tr)
	if err != nil {
		t.Fatalf("vlan verify Fetch: %v", err)
	}
	if ops, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": vlanID, "name": name}}}, verify); err != nil || len(ops) != 0 {
		t.Fatalf("vlan verify diff ops=%v err=%v", ops, err)
	}
}

func newLiveNXOSConfigTransport(t *testing.T) transport.Interface {
	t.Helper()
	address := os.Getenv("NXOS_LIVE_ADDRESS")
	username := os.Getenv("NXOS_LIVE_USERNAME")
	password := os.Getenv("NXOS_LIVE_PASSWORD")
	if address == "" || username == "" || password == "" {
		t.Fatal("NXOS_LIVE_ADDRESS, NXOS_LIVE_USERNAME, and NXOS_LIVE_PASSWORD are required")
	}
	port := 0
	if raw := os.Getenv("NXOS_LIVE_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("NXOS_LIVE_PORT: %v", err)
		}
		port = p
	}
	tlsEnabled := liveBoolEnv(t, "NXOS_LIVE_TLS")
	insecureSkipVerify := liveBoolEnv(t, "NXOS_LIVE_INSECURE_SKIP_VERIFY")
	tr, err := newNXAPIConfigTransport(&ciskov1.DeviceSpec{
		Driver:   ciskov1.DeviceDriverNXOS,
		Address:  address,
		Port:     port,
		Username: username,
		Password: password,
		TLS: &ciskov1.TLSConfig{
			Enabled:            tlsEnabled,
			InsecureSkipVerify: insecureSkipVerify,
		},
	})
	if err != nil {
		t.Fatalf("newNXAPIConfigTransport: %v", err)
	}
	return tr
}

func parseLiveNXOSVLANID(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("value is required")
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("want an integer from %d through %d: %w", liveNXOSMinMutableVLAN, liveNXOSMaxMutableVLAN, err)
	}
	if id < liveNXOSMinMutableVLAN || id > liveNXOSMaxMutableVLAN {
		return 0, fmt.Errorf("%d is outside the permitted mutable range %d through %d", id, liveNXOSMinMutableVLAN, liveNXOSMaxMutableVLAN)
	}
	return id, nil
}

type reservedVLANDMEReader interface {
	FetchDME(context.Context, string) ([]byte, error)
}

type nxapiReservedVLANDMEReader struct {
	transport *nxapiConfigTransport
}

func (r nxapiReservedVLANDMEReader) FetchDME(ctx context.Context, dn string) ([]byte, error) {
	if r.transport == nil || r.transport.client == nil {
		return nil, errors.New("NX-API transport is not initialized")
	}
	return r.transport.dmeGetWithRetry(ctx, dn, nil)
}

func requireReservedVLANAbsent(ctx context.Context, reader reservedVLANDMEReader, vlanID int) error {
	if vlanID < liveNXOSMinMutableVLAN || vlanID > liveNXOSMaxMutableVLAN {
		return fmt.Errorf("reserved VLAN ID %d is outside the permitted mutable range %d through %d", vlanID, liveNXOSMinMutableVLAN, liveNXOSMaxMutableVLAN)
	}
	if reader == nil {
		return errors.New("reserved VLAN DME reader is required")
	}
	dn := fmt.Sprintf("%s/bd-[vlan-%d]", nxosschema.DNBridgeDomain, vlanID)
	raw, err := reader.FetchDME(ctx, dn)
	if err != nil {
		return fmt.Errorf("fetch exact reserved VLAN DME object: %w", err)
	}
	return validateExactReservedVLANDME(raw, vlanID)
}

func validateExactReservedVLANDME(raw []byte, reservedID int) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("malformed exact VLAN DME response: %w", err)
	}
	if envelope == nil {
		return errors.New("malformed exact VLAN DME response: want object")
	}
	rawCount, ok := envelope["totalCount"]
	if !ok {
		return errors.New("malformed exact VLAN DME response: missing totalCount")
	}
	var count string
	if err := json.Unmarshal(rawCount, &count); err != nil || (count != "0" && count != "1") {
		return fmt.Errorf("malformed exact VLAN DME response: totalCount must be string \"0\" or \"1\"")
	}

	rawItems, ok := envelope["imdata"]
	if !ok {
		return errors.New("malformed exact VLAN DME response: missing imdata")
	}
	if len(envelope) != 2 {
		return errors.New("malformed exact VLAN DME response: unexpected top-level fields")
	}
	if !strings.HasPrefix(strings.TrimSpace(string(rawItems)), "[") {
		return errors.New("malformed exact VLAN DME response: imdata must be an array")
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(rawItems, &items); err != nil {
		return fmt.Errorf("malformed exact VLAN DME response: imdata: %w", err)
	}
	wantItems := 0
	if count == "1" {
		wantItems = 1
	}
	if len(items) != wantItems {
		return fmt.Errorf("malformed exact VLAN DME response: totalCount=%s but imdata has %d items", count, len(items))
	}
	if count == "0" {
		return nil
	}

	item := items[0]
	if len(item) != 1 {
		return fmt.Errorf("malformed exact VLAN DME response: imdata[0] must contain exactly one l2BD object")
	}
	rawBD, ok := item["l2BD"]
	if !ok {
		return errors.New("malformed exact VLAN DME response: imdata[0] is not an l2BD object")
	}
	var bd struct {
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	if err := json.Unmarshal(rawBD, &bd); err != nil || bd.Attributes == nil {
		return errors.New("malformed exact VLAN DME response: l2BD attributes are missing or invalid")
	}
	rawEncap, ok := bd.Attributes["fabEncap"]
	if !ok {
		return errors.New("malformed exact VLAN DME response: l2BD fabEncap is missing")
	}
	var encap string
	if err := json.Unmarshal(rawEncap, &encap); err != nil {
		return errors.New("malformed exact VLAN DME response: l2BD fabEncap is not a string")
	}
	wantEncap := fmt.Sprintf("vlan-%d", reservedID)
	if encap != wantEncap {
		return fmt.Errorf("malformed exact VLAN DME response: l2BD fabEncap does not match reserved VLAN %d", reservedID)
	}
	return fmt.Errorf("reserved VLAN %d is already present", reservedID)
}

func TestParseLiveNXOSVLANID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "reserved lab VLAN", raw: "3903", want: 3903},
		{name: "lower bound", raw: "2", want: 2},
		{name: "upper bound", raw: "3967", want: 3967},
		{name: "missing", wantErr: true},
		{name: "not numeric", raw: "vlan-3903", wantErr: true},
		{name: "default VLAN is not mutable", raw: "1", wantErr: true},
		{name: "default internal range", raw: "3968", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLiveNXOSVLANID(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLiveNXOSVLANID(%q) error=%v, wantErr=%t", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseLiveNXOSVLANID(%q)=%d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestRequireReservedVLANAbsent(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		fetchErr   error
		reservedID int
		wantErr    string
	}{
		{
			name:       "exact object absent",
			raw:        `{"totalCount":"0","imdata":[]}`,
			reservedID: 3903,
		},
		{
			name:       "exact object present",
			raw:        `{"totalCount":"1","imdata":[{"l2BD":{"attributes":{"fabEncap":"vlan-3903","name":"occupied"}}}]}`,
			reservedID: 3903,
			wantErr:    "reserved VLAN 3903 is already present",
		},
		{
			name:       "missing totalCount",
			raw:        `{"imdata":[]}`,
			reservedID: 3903,
			wantErr:    "missing totalCount",
		},
		{
			name:       "unexpected top-level field",
			raw:        `{"totalCount":"0","imdata":[],"warning":"partial"}`,
			reservedID: 3903,
			wantErr:    "unexpected top-level fields",
		},
		{
			name:       "numeric totalCount",
			raw:        `{"totalCount":0,"imdata":[]}`,
			reservedID: 3903,
			wantErr:    `totalCount must be string "0" or "1"`,
		},
		{
			name:       "unsupported totalCount",
			raw:        `{"totalCount":"2","imdata":[]}`,
			reservedID: 3903,
			wantErr:    `totalCount must be string "0" or "1"`,
		},
		{
			name:       "missing imdata",
			raw:        `{"totalCount":"0"}`,
			reservedID: 3903,
			wantErr:    "missing imdata",
		},
		{
			name:       "null imdata",
			raw:        `{"totalCount":"0","imdata":null}`,
			reservedID: 3903,
			wantErr:    "imdata must be an array",
		},
		{
			name:       "zero count with row",
			raw:        `{"totalCount":"0","imdata":[{"l2BD":{"attributes":{"fabEncap":"vlan-3903"}}}]}`,
			reservedID: 3903,
			wantErr:    "totalCount=0 but imdata has 1 items",
		},
		{
			name:       "one count without row",
			raw:        `{"totalCount":"1","imdata":[]}`,
			reservedID: 3903,
			wantErr:    "totalCount=1 but imdata has 0 items",
		},
		{
			name:       "wrong managed object class",
			raw:        `{"totalCount":"1","imdata":[{"topSystem":{"attributes":{"id":"1"}}}]}`,
			reservedID: 3903,
			wantErr:    "is not an l2BD object",
		},
		{
			name:       "wrong exact VLAN identity",
			raw:        `{"totalCount":"1","imdata":[{"l2BD":{"attributes":{"fabEncap":"vlan-3904"}}}]}`,
			reservedID: 3903,
			wantErr:    "fabEncap does not match reserved VLAN 3903",
		},
		{
			name:       "malformed JSON",
			raw:        `{"totalCount":`,
			reservedID: 3903,
			wantErr:    "malformed exact VLAN DME response",
		},
		{
			name:       "exact DME fetch failure",
			fetchErr:   errors.New("read denied"),
			reservedID: 3903,
			wantErr:    "fetch exact reserved VLAN DME object: read denied",
		},
		{
			name:       "invalid requested ID",
			raw:        `{"totalCount":"0","imdata":[]}`,
			reservedID: 3968,
			wantErr:    "outside the permitted mutable range",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &recordingReservedVLANDMEReader{raw: []byte(tt.raw), fetchErr: tt.fetchErr}
			err := requireReservedVLANAbsent(context.Background(), reader, tt.reservedID)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("requireReservedVLANAbsent: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("requireReservedVLANAbsent error=%v, want substring %q", err, tt.wantErr)
			}
			if tt.reservedID >= liveNXOSMinMutableVLAN && tt.reservedID <= liveNXOSMaxMutableVLAN {
				wantDN := fmt.Sprintf("%s/bd-[vlan-%d]", nxosschema.DNBridgeDomain, tt.reservedID)
				if len(reader.dns) != 1 || reader.dns[0] != wantDN {
					t.Fatalf("DME reads=%v, want [%s]", reader.dns, wantDN)
				}
			} else if len(reader.dns) != 0 {
				t.Fatalf("invalid requested ID read DNs %v", reader.dns)
			}
		})
	}
}

func TestNXAPIReservedVLANDMEReaderUsesExactDMETransport(t *testing.T) {
	var loginCount, readCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dmeLoginPath:
			loginCount++
			if r.Method != http.MethodPost {
				t.Errorf("DME login method=%s, want POST", r.Method)
			}
			http.SetCookie(w, &http.Cookie{Name: "nxapi_auth", Value: "test-token"})
			_, _ = w.Write([]byte(`{"aaaLogin":{"attributes":{"token":"test-token"}}}`))
		case "/api/mo/sys/bd/bd-[vlan-3903].json":
			readCount++
			if r.Method != http.MethodGet {
				t.Errorf("exact VLAN method=%s, want GET", r.Method)
			}
			if _, err := r.Cookie("nxapi_auth"); err != nil {
				t.Errorf("exact VLAN read has no DME login cookie: %v", err)
			}
			if r.URL.RawQuery != "" {
				t.Errorf("exact VLAN query=%q, want empty", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"totalCount":"0","imdata":[]}`))
		default:
			t.Errorf("unexpected NX-API path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tr := &nxapiConfigTransport{client: &nxapiClient{
		rootURL:  server.URL,
		baseURL:  server.URL + nxapiPath,
		username: "readonly",
		password: "test-only",
		client:   server.Client(),
	}}
	reader := nxapiReservedVLANDMEReader{transport: tr}
	if err := requireReservedVLANAbsent(context.Background(), reader, 3903); err != nil {
		t.Fatalf("requireReservedVLANAbsent through real NX-API transport: %v", err)
	}
	if loginCount != 1 || readCount != 1 {
		t.Fatalf("DME calls login=%d read=%d, want 1 each", loginCount, readCount)
	}
}

func TestLiveNXOSReservedVLANAbsentMarkerIsStable(t *testing.T) {
	if got, want := fmt.Sprintf("%s=%d", liveNXOSReservedVLANAbsentMarker, 3903), "CVK_NXOS_RESERVED_VLAN_ABSENT=3903"; got != want {
		t.Fatalf("marker=%q, want %q", got, want)
	}
}

type recordingReservedVLANDMEReader struct {
	raw      []byte
	fetchErr error
	dns      []string
}

func (r *recordingReservedVLANDMEReader) FetchDME(_ context.Context, dn string) ([]byte, error) {
	r.dns = append(r.dns, dn)
	return r.raw, r.fetchErr
}

func liveBoolEnv(t *testing.T, key string) bool {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return v
}

func vlanPresent(observed any, id int) bool {
	_, ok := vlanByID(observed, id)
	return ok
}

func vlanByID(observed any, id int) (map[string]any, bool) {
	m, ok := observed.(map[string]any)
	if !ok {
		return nil, false
	}
	list, ok := m["vlans"].([]any)
	if !ok {
		return nil, false
	}
	for _, item := range list {
		vm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch v := vm["id"].(type) {
		case int:
			if v == id {
				return vm, true
			}
		case float64:
			if int(v) == id {
				return vm, true
			}
		}
	}
	return nil, false
}
